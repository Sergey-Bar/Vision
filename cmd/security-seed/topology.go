package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
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

// The devices the committed fixtures name.
//
// They come out of the fixtures' `Source` fields — "Entrance Camera 01 [CAM-001]",
// "Loading Bay Door [DOR-004]", "Perimeter North [CAM-014]" — and a device whose
// `source_device_id` is not registered produces an event with no device. That is
// correct behaviour ("unattributable" rather than attributed to something
// guessed) and it would make the whole seeded site assert nothing, because a rule
// that filters on a zone never gets there.
//
// So this list is derived from the fixtures rather than invented, and the seed
// prints it. If a fixture gains a device reference, the seed says so rather than
// quietly attributing the event to the nearest camera it knows.
var fixtureDevices = []struct {
	sourceDeviceID string
	deviceID       string
	zoneID         string
	kind           string
	name           string
}{
	{"CAM-001", "dev-entrance-01", "zone-restricted-bay", "camera", "Entrance Camera 01"},
	{"CAM-014", "dev-perimeter-north", "zone-perimeter", "camera", "Perimeter North"},
	{"DOR-004", "dev-loading-bay-door", "zone-restricted-bay", "door", "Loading Bay Door"},
}

// The zones the shipped bundle's one production rule needs.
//
// `core.person-in-restricted-zone-after-hours` filters on
// `zone.pathPrefix: /restricted`, and `pathHasPrefix` matches a contiguous run of
// WHOLE SEGMENTS anywhere in the path rather than a literal string prefix — so
// `/north-gate/restricted/loading-bay` carries the run `/restricted` and matches,
// and `/north-gate/restricted-bay` would not. A seed with the wrong path here
// makes the bundle's one production rule dead rather than failing, which is the
// specific outcome `internal/rules/semantic.go` exists to prevent.
//
// Depth is written because the column is `CHECK (depth >= 0)` and because a zone
// tree with a wrong depth is a zone tree the recursive traversal gets wrong.
var fixtureZones = []struct {
	zoneID, name, path, parent string
	depth                      int
}{
	{"zone-site", "North Gate", "/north-gate", "", 0},
	{"zone-restricted", "Restricted", "/north-gate/restricted", "zone-site", 1},
	{"zone-restricted-bay", "Loading Bay", "/north-gate/restricted/loading-bay", "zone-restricted", 2},
	{"zone-perimeter", "Perimeter", "/north-gate/perimeter", "zone-site", 1},
}

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

	for _, z := range fixtureZones {
		var parent any
		if z.parent != "" {
			parent = z.parent
		}
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO zones (tenant_id, zone_id, site_id, parent_id, name, path, depth)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, zone_id) DO NOTHING`,
			s.opts.tenant, z.zoneID, s.opts.site, parent, z.name, z.path, z.depth); err != nil {
			return fmt.Errorf("insert the zone %s: %w", z.zoneID, err)
		}
	}

	for _, d := range fixtureDevices {
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO devices (tenant_id, device_id, site_id, zone_id, kind, display_name,
			                     attributes, source_device_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (tenant_id, device_id) DO NOTHING`,
			s.opts.tenant, d.deviceID, s.opts.site, d.zoneID, d.kind, d.name,
			`{"tags":["perimeter"]}`, d.sourceDeviceID); err != nil {
			return fmt.Errorf("insert the device %s: %w", d.deviceID, err)
		}
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO cameras (tenant_id, camera_id, device_id, site_id, source_camera_id,
			                     display_name, state, recording_state)
			VALUES ($1, $2, $3, $4, $5, $6, 'online', 'recording')
			ON CONFLICT (tenant_id, camera_id) DO NOTHING`,
			s.opts.tenant, "cam-"+d.sourceDeviceID, d.deviceID, s.opts.site,
			d.sourceDeviceID, d.name); err != nil {
			return fmt.Errorf("insert the camera %s: %w", d.sourceDeviceID, err)
		}
		// A clock row per device. `device_clocks.drift_ms` is read by the gauge
		// sampler and by the correlator's exclusion list; a device with no row has
		// no drift reading, and the SLO's drift objective is then permanently
		// "not measured" on a fresh install.
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO device_clocks (tenant_id, device_id, site_id, clock_source, drift_ms, band)
			VALUES ($1, $2, $3, 'ntp', 0, 'ok')
			ON CONFLICT (tenant_id, device_id) DO NOTHING`,
			s.opts.tenant, d.deviceID, s.opts.site); err != nil {
			return fmt.Errorf("insert the clock for %s: %w", d.deviceID, err)
		}
	}
	return nil
}
