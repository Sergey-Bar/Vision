package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/fixture"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/simulate"
)

// The tenant these commands act on.
//
// It comes from the site configuration, never from a flag or a request body. An
// operator who can name any tenant on a command line can act on any tenant, which
// is the shape of a vulnerability rather than a feature.
func configuredTenant() string {
	return dbx.EnvOrDefault("SP_TENANT_ID", "tenant-local")
}

// newSiteCompiler builds a rule compiler that knows this site's zones and
// schedules, which is what turns "this zone does not exist" from a guess into a
// finding. Without a database it degrades to the closed vocabulary alone, and the
// compiler warns on anything it cannot check rather than pretending.
func newSiteCompiler() *rules.Compiler {
	c := rules.NewCompiler()
	c.KnownAIFields = aiProfileFields()
	return c
}

func newBundleService(ctx context.Context) *bundles.Service {
	pool, err := openPool(ctx)
	if err != nil {
		return nil
	}
	return &bundles.Service{
		Pool:     pool,
		Compiler: newSiteCompiler(),
		Audit:    &audit.Writer{Pool: pool},
	}
}

func openPool(ctx context.Context) (*dbx.Pool, error) {
	cfg := dbx.DefaultConfig()
	if v := os.Getenv("SP_DATABASE_URL"); v != "" {
		if err := cfg.ApplyDSN(v); err != nil {
			return nil, err
		}
	}
	return dbx.Open(ctx, cfg, os.Getenv("SP_DATABASE_PASSWORD"))
}

func activeBundle(ctx context.Context) (string, []byte, error) {
	pool, err := openPool(ctx)
	if err != nil {
		return "", nil, err
	}
	defer pool.Close()
	svc := &bundles.Service{Pool: pool}
	return svc.Active(ctx, configuredTenant())
}

// businessHoursSchedule resolves the schedule the built-in bundle names.
//
// It delegates to `rules.BusinessHoursSchedule` so the simulation and the
// production pipeline answer the same question. It used to carry its own copy,
// hardcoded to UTC: three callers read a schedule and each supplied a different
// answer, and the one that mattered most — the production pipeline — supplied
// none at all, so every schedule rule in the active bundle was dead. A digest
// acknowledged at activation then certified a behaviour the product did not have.
//
// Schedules are site data rather than bundle data, so they live in one place an
// operator edits rather than in every rule. A site with different hours supplies
// its own resolver; the default is 08:00 to 18:00.
func businessHoursSchedule(id string, at time.Time) bool {
	return rules.BusinessHoursSchedule(id, at, time.UTC)
}

// scenariosFromFixtures turns a fixture set into simulate scenarios.
//
// The path is the real one: fixtures are replayed through the Milestone adapter,
// so a scenario's signals are what the adapter would have produced against a live
// server. Anything else would be a second code path, and a second code path is
// exactly what replay equality is meant to rule out.
func scenariosFromFixtures(ctx context.Context, dir string) ([]simulate.Scenario, error) {
	store, err := fixture.NewStore(dir)
	if err != nil {
		return nil, fmt.Errorf("rules simulate: %w", err)
	}
	if _, err := store.VerifyManifest(); err != nil {
		return nil, fmt.Errorf("rules simulate: %w", err)
	}
	scenarios, err := store.All()
	if err != nil {
		return nil, fmt.Errorf("rules simulate: %w", err)
	}

	// A fixed clock so a simulation is reproducible: an operator who runs this
	// twice to confirm a review must get the same answer.
	replayEpoch := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)

	out := make([]simulate.Scenario, 0, len(scenarios))
	for _, sc := range scenarios {
		transport := milestone.NewReplayTransport([]fixture.Scenario{sc}).WithEpoch(replayEpoch)
		adapter := milestone.New(transport, sc.SiteID, "simulate").
			WithClock(func() time.Time { return replayEpoch })

		records, err := adapter.Poll(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("rules simulate: replay %s: %w", sc.Name, err)
		}

		signals := make([]rules.Signal, 0, len(records))
		for _, r := range records {
			if r.SourceEventID == "" {
				// A feed error or an unparsable item. Recorded as a signal with no
				// source id so a simulate diff shows that the fixture produced one,
				// rather than quietly skipping it.
				continue
			}
			signals = append(signals, signalFromRecord(r))
		}
		if len(signals) == 0 {
			continue
		}
		out = append(out, simulate.Scenario{ID: sc.Name, Signals: signals})
	}
	return out, nil
}

// signalFromRecord maps a replayed source record into a rule-engine signal.
func signalFromRecord(r milestone.RawRecord) rules.Signal {
	observed := r.ObservedAt
	if observed.IsZero() {
		observed = r.ReceivedAt
	}
	var payload map[string]any
	_ = json.Unmarshal(r.Envelope, &payload)

	return rules.Signal{
		EventID:       r.SourceEventID,
		SourceEventID: r.SourceEventID,
		EventType:     r.EventType,
		NormalizedAt:  observed,
		DeviceID:      r.SourceDeviceRef,
		Tier:          rules.TierA,
		Payload:       payload,
	}
}
