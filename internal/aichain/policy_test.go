package aichain_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/aichain"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The policy and the egress log, over a real database.
//
// The chain's BEHAVIOUR is tested without a database, in chain_test.go. What
// needs a database is the half that is a fact about the SITE: which providers
// the site may use, in what order, and what left the site when they were
// used.

// service returns the chain service over the package's own database.
func service(t *testing.T) (*aichain.Service, *dbx.Pool, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply the DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	tenant, site, _ := testutil.NewTenant(t, raw)
	return aichain.NewService(pool), pool, tenant, site
}

// writePolicy writes a site's chain.
func writePolicy(t *testing.T, pool *dbx.Pool, tenant, site string, egress bool, specs []aichain.ProviderSpec) {
	t.Helper()
	raw, err := json.Marshal(specs)
	if err != nil {
		t.Fatalf("marshal the chain: %v", err)
	}
	_, err = pool.WithSites(tenant, "ai-chain", []string{site}).Exec(context.Background(), `
		INSERT INTO ai_policies (tenant_id, site_id, providers, egress_allowed, timeout_seconds)
		VALUES ($1, $2, $3, $4, 30)`,
		tenant, site, raw, egress)
	if err != nil {
		t.Fatalf("write the policy: %v", err)
	}
}

func TestThePolicyNamesTheChainInOrder(t *testing.T) {
	svc, pool, tenant, site := service(t)
	writePolicy(t, pool, tenant, site, true, []aichain.ProviderSpec{
		{Kind: aichain.KindLocalBundle, BaseURL: "http://127.0.0.1:8080", Model: "qwen2.5-vl"},
		{Kind: aichain.KindOpenAICompatible, BaseURL: "https://api.example.com/v1", Model: "gpt-4o"},
	})

	policy, err := svc.Policy(context.Background(), tenant, site)
	if err != nil {
		t.Fatalf("read the policy: %v", err)
	}
	if len(policy.Providers) != 2 {
		t.Fatalf("the chain holds %d provider(s), want 2", len(policy.Providers))
	}
	// The ORDER is the policy, and it is the order the chain is tried in.
	if policy.Providers[0].Kind != aichain.KindLocalBundle {
		t.Errorf("the first provider is %q, want the local bundle", policy.Providers[0].Kind)
	}
	if policy.Providers[1].Kind != aichain.KindOpenAICompatible {
		t.Errorf("the second provider is %q, want the hosted one", policy.Providers[1].Kind)
	}
}

func TestASiteWithNoPolicyIsRefused(t *testing.T) {
	// "This site has no brain configured" and "this site's brain is empty"
	// are different facts, and only one of them is a working product.
	svc, _, tenant, site := service(t)
	_, err := svc.Policy(context.Background(), tenant, site)
	if err == nil {
		t.Fatal("a site with no policy was read")
	}
	if !strings.Contains(err.Error(), "no rows") {
		t.Logf("the refusal is the database's: %v", err)
	}
	_, err = svc.ChainFor(context.Background(), tenant, site)
	if err == nil {
		t.Fatal("a site with no policy built a chain")
	}
}

func TestAHostedProviderIsRefusedWhenTheSiteMayNotSendAnythingOffTheMachine(t *testing.T) {
	// The rule is enforced when the chain is BUILT, in front of a person —
	// not at the moment the first observation is due.
	svc, pool, tenant, site := service(t)
	writePolicy(t, pool, tenant, site, false, []aichain.ProviderSpec{
		{Kind: aichain.KindAnthropic, BaseURL: "https://api.anthropic.com", Model: "claude"},
	})

	_, err := svc.ChainFor(context.Background(), tenant, site)
	if err == nil {
		t.Fatal("a hosted provider was accepted by a site that may not send anything off the machine")
	}
	if !strings.Contains(err.Error(), "may not send evidence off the machine") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestALocalBundleIsAcceptedWithoutTheEgressPermission(t *testing.T) {
	// A bundle on this machine is not egress. The rule is about the
	// ADDRESS, and the adapter checks that.
	svc, pool, tenant, site := service(t)
	writePolicy(t, pool, tenant, site, false, []aichain.ProviderSpec{
		{Kind: aichain.KindLocalBundle, BaseURL: "http://127.0.0.1:8080", Model: "qwen2.5-vl"},
	})
	chain, err := svc.ChainFor(context.Background(), tenant, site)
	if err != nil {
		t.Fatalf("a local bundle was refused: %v", err)
	}
	if len(chain.Providers()) != 1 {
		t.Fatalf("the chain holds %d provider(s), want 1", len(chain.Providers()))
	}
}

func TestAnUnknownKindIsRefusedByName(t *testing.T) {
	svc, pool, tenant, site := service(t)
	writePolicy(t, pool, tenant, site, true, []aichain.ProviderSpec{
		{Kind: "some_kind_this_build_does_not_know", BaseURL: "http://127.0.0.1:8080"},
	})
	_, err := svc.ChainFor(context.Background(), tenant, site)
	if err == nil {
		t.Fatal("an unknown adapter kind was accepted")
	}
	if !strings.Contains(err.Error(), "does not implement") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestTheEgressLogRecordsTheDigestsAndNotTheContent(t *testing.T) {
	// The whole point of the table: a log of what left the site that a
	// customer can read and an auditor can check. It records the digests,
	// and it does NOT keep the bytes.
	svc, _, tenant, site := service(t)

	err := svc.RecordEgress(context.Background(), aichain.Egress{
		TenantID:      tenant,
		SiteID:        site,
		EgressID:      "egr-1",
		Provider:      aichain.KindOpenAICompatible,
		ObservationID: "obs-1",
		ProfileID:     "general-monitoring",
		EvidenceCount: 2,
		EvidenceBytes: 4096,
		EvidenceHashes: []string{
			"aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999",
		},
		RequestedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("record the egress: %v", err)
	}

	records, err := svc.EgressLog(context.Background(), tenant, site, 10)
	if err != nil {
		t.Fatalf("read the egress log: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("the log holds %d record(s), want 1", len(records))
	}
	got := records[0]
	if got.Provider != aichain.KindOpenAICompatible {
		t.Errorf("the record names %q, want the provider that answered", got.Provider)
	}
	if got.EvidenceCount != 2 || got.EvidenceBytes != 4096 {
		t.Errorf("the record carries %d evidence(s) and %d bytes, want 2 and 4096",
			got.EvidenceCount, got.EvidenceBytes)
	}
	if len(got.EvidenceHashes) != 1 {
		t.Fatalf("the record carries %d digest(s), want 1", len(got.EvidenceHashes))
	}
	// And the CONTENT is not there, because the table has no column that
	// could hold it.
	if strings.Contains(got.EvidenceHashes[0], "person") {
		t.Error("the record carries something other than a digest")
	}
}

func TestTheEgressLogIsScopedToItsSite(t *testing.T) {
	// A log another site can read is a log that tells a competitor what left
	// this one.
	svc, _, tenant, site := service(t)
	other := site + "-other"
	if _, err := svc.Pool.WithSites(tenant, "ai-chain", []string{other}).Exec(context.Background(),
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, 'Other', 'UTC')`,
		other, tenant); err != nil {
		t.Fatalf("seed the second site: %v", err)
	}

	for _, s := range []string{site, other} {
		err := svc.RecordEgress(context.Background(), aichain.Egress{
			TenantID: tenant, SiteID: s, EgressID: "egr-" + s,
			Provider: aichain.KindLocalBundle, ObservationID: "obs-1",
			ProfileID: "general-monitoring", EvidenceCount: 1, EvidenceBytes: 10,
			EvidenceHashes: []string{}, RequestedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("record the egress: %v", err)
		}
	}

	records, err := svc.EgressLog(context.Background(), tenant, site, 10)
	if err != nil {
		t.Fatalf("read the egress log: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("the log holds %d record(s), want 1: the other site's record leaked", len(records))
	}
	if records[0].SiteID != site {
		t.Errorf("the record is for %q, want %q", records[0].SiteID, site)
	}
}
