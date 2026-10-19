package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/fixture"
)

// The topology: tenant, site, integration, zones, devices, clocks.
//
// Every row here is written through the owning service or through the site's own
// tables, never through a script of raw INSERTs that the product does not also
// use. A seed that bypassed an owner's validation would produce a site the
// product could not have created, and every later claim about that site would be
// a claim about a fiction.
//
// `ON CONFLICT DO NOTHING` is used throughout, which makes the seed idempotent
// rather than merely repeatable. `--reset` exists for the case where a previous
// run left something behind, and it refuses to run against a tenant it did not
// create.

// The devices and the zones live in the package that owns the scenarios.
//
// They were here, and the SIMULATION could not see them: the simulation is the
// gate every bundle change is measured against, and it built its signals
// without a zone, so every rule that filters on one looked dead. The seed
// wrote a topology and the gate read none, and a change that only differed by
// zone looked like no change at all.
//
// One table, two readers: the seed writes it into a real database, and the
// simulation reads it to answer the same question the database answers in
// production.

// writeTopology creates the tenant, the site, the integration and the physical
// topology the fixtures refer to.
func (s *seeder) writeTopology(ctx context.Context) error {
	// The tenant. `tenants` carries tenant_id and therefore row-level security,
	// so this runs as a system worker: a seed is the installer, and the installer
	// runs before anybody is authenticated. Under the ordinary tenant predicate
	// the row would be refused, which is the predicate working.
	err := s.pool.InTenant(ctx,
		dbx.TenantContext{TenantID: s.opts.tenant, ActorID: "seed", System: true},
		func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `
				INSERT INTO tenants (tenant_id, name) VALUES ($1, $2)
				ON CONFLICT (tenant_id) DO NOTHING`,
				s.opts.tenant, "Northwind Facilities"); err != nil {
				return fmt.Errorf("insert the tenant: %w", err)
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO sites (site_id, tenant_id, name, timezone, site_tags)
				VALUES ($1, $2, $3, $4, '["perimeter","loading-bay"]'::jsonb)
				ON CONFLICT (tenant_id, site_id) DO NOTHING`,
				s.opts.site, s.opts.tenant, "North Gate", s.opts.timezone)
			return err
		})
	if err != nil {
		return err
	}

	// The integration. This row is not decoration: the ingest loop reads
	// `last_success_at` from it as the poll cursor and writes the newest accepted
	// instant back after every pass. Without it every poll is a full re-read.
	if _, err := s.scoped(ctx).Exec(ctx, `
		INSERT INTO integrations (tenant_id, integration_id, site_id, type, enabled, base_url)
		VALUES ($1, $2, $3, 'xprotect_milestone', true, $4)
		ON CONFLICT (tenant_id, integration_id) DO NOTHING`,
		s.opts.tenant, s.opts.integration, s.opts.site,
		"http://127.0.0.1:8000/api"); err != nil {
		return fmt.Errorf("insert the integration: %w", err)
	}

	for _, z := range fixture.Zones {
		var parent any
		if z.Parent != "" {
			parent = z.Parent
		}
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO zones (tenant_id, zone_id, site_id, parent_id, name, path, depth)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, zone_id) DO NOTHING`,
			s.opts.tenant, z.ZoneID, s.opts.site, parent, z.Name, z.Path, z.Depth); err != nil {
			return fmt.Errorf("insert the zone %s: %w", z.ZoneID, err)
		}
	}

	for _, d := range fixture.Devices {
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO devices (tenant_id, device_id, site_id, zone_id, kind, display_name,
			                     attributes, source_device_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (tenant_id, device_id) DO NOTHING`,
			s.opts.tenant, d.DeviceID, s.opts.site, d.ZoneID, d.Kind, d.Name,
			`{"tags":["perimeter"]}`, d.SourceDeviceID); err != nil {
			return fmt.Errorf("insert the device %s: %w", d.DeviceID, err)
		}
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO cameras (tenant_id, camera_id, device_id, site_id, source_camera_id,
			                     display_name, state, recording_state)
			VALUES ($1, $2, $3, $4, $5, $6, 'online', 'recording')
			ON CONFLICT (tenant_id, camera_id) DO NOTHING`,
			s.opts.tenant, "cam-"+d.SourceDeviceID, d.DeviceID, s.opts.site,
			d.SourceDeviceID, d.Name); err != nil {
			return fmt.Errorf("insert the camera %s: %w", d.SourceDeviceID, err)
		}
		// A clock row per device. `device_clocks.drift_ms` is read by the gauge
		// sampler and by the correlator's exclusion list; a device with no row has
		// no drift reading, and the SLO's drift objective is then permanently
		// "not measured" on a fresh install.
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO device_clocks (tenant_id, device_id, site_id, clock_source, drift_ms, band)
			VALUES ($1, $2, $3, 'ntp', 0, 'ok')
			ON CONFLICT (tenant_id, device_id) DO NOTHING`,
			s.opts.tenant, d.DeviceID, s.opts.site); err != nil {
			return fmt.Errorf("insert the clock for %s: %w", d.DeviceID, err)
		}
	}
	return nil
}
