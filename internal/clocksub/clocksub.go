// Package clocksub measures and stores per-device clock drift.
//
// Clock correctness is load-bearing: door plus camera plus access correlation
// breaks without it. A site whose cameras are 40 seconds apart will report a
// person entering a restricted zone and the door opening as two unrelated
// events, and an operator who cannot trust the timeline stops trusting the
// product.
//
// The measurement is differential. Every source has a clock; the question is not
// whether any of them is right but how far each one is from the site's reference.
// A device is never labelled "wrong", only "far from the reference by this much".
package clocksub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// Service measures and records drift.
type Service struct {
	Pool *dbx.Pool
	// Reference is the clock everything is measured against. Injected rather than
	// read from a global, because two sites legitimately have different answers
	// and hard-coding one would silently apply one site's assumptions to another.
	Reference clock.Source
	// Now is injectable so a test can drive measurements deterministically.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Observation is one device's reported time alongside the reference's.
type Observation struct {
	TenantID string
	DeviceID string
	SiteID   string
	// Reported is what the device said the time was.
	Reported time.Time
	// Source is what the device said its clock was.
	Source clock.Source
	// ReferenceID names the reference the comparison was made against, so a
	// measurement can be traced to the clock it used.
	ReferenceID string
}

// ErrUnknownDevice is returned when an observation names a device the site has
// never registered. Measuring drift for a device the platform does not know
// about would record a number nobody can act on.
var ErrUnknownDevice = errors.New("clocksub: unknown device")

// Measure records one observation and returns the resulting state.
//
// The drift is the difference between what the device reported for an instant and
// what the reference said that instant was, so a positive drift means the device
// runs ahead. Correction is applied at normalization time by subtracting it.
func (s *Service) Measure(ctx context.Context, policy clock.Policy, obs Observation) (clock.DeviceState, error) {
	var state clock.DeviceState
	if err := policy.Validate(); err != nil {
		return state, err
	}
	if obs.DeviceID == "" {
		return state, errors.New("clocksub: device id is required")
	}
	if obs.Reported.IsZero() {
		return state, errors.New("clocksub: a device with no reported time cannot be measured")
	}

	now := s.now()
	referenceNow := now
	driftMS := int32(obs.Reported.Sub(referenceNow) / time.Millisecond)

	source := obs.Source
	if source == "" {
		source = clock.SourceUnknown
	}

	state = clock.DeviceState{
		TenantID:    obs.TenantID,
		DeviceID:    obs.DeviceID,
		SiteID:      obs.SiteID,
		Source:      source,
		DriftMs:     driftMS,
		Band:        policy.Band(driftMS),
		ReferenceID: obs.ReferenceID,
		MeasuredAt:  now,
	}

	err := s.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: obs.TenantID,
		ActorID:  "clocksub",
	}, func(tx pgx.Tx) error {
		// The device must exist. Recording drift for an unregistered device
		// would produce a row nothing reads, and a rule excluding it from
		// corroboration would silently drop events.
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM devices WHERE tenant_id = $1 AND device_id = $2`,
			obs.TenantID, obs.DeviceID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrUnknownDevice, obs.DeviceID)
			}
			return fmt.Errorf("clocksub: look up device: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO device_clocks (tenant_id, device_id, site_id, clock_source, drift_ms, band, reference_id, measured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (tenant_id, device_id) DO UPDATE
			   SET clock_source = EXCLUDED.clock_source,
			       drift_ms = EXCLUDED.drift_ms,
			       band = EXCLUDED.band,
			       reference_id = EXCLUDED.reference_id,
			       measured_at = EXCLUDED.measured_at`,
			obs.TenantID, obs.DeviceID, obs.SiteID, string(source),
			driftMS, string(state.Band), obs.ReferenceID, now); err != nil {
			return fmt.Errorf("clocksub: record drift: %w", err)
		}
		return nil
	})
	return state, err
}

// MeasureFromSourceTimes records drift from a source system's own reported time
// rather than a device's.
//
// This is the VMS case: the whole system is the clock, and its drift is measured
// against the site's reference. A VMS 40 seconds fast is not one bad camera, it
// is every event it produced, which is why the plan asks for skew to be injected
// on a camera *and* a VMS and correlation to be checked against both.
func (s *Service) MeasureFromSourceTimes(ctx context.Context, policy clock.Policy, tenant, site, integration, referenceID string, sourceReported, referenceNow time.Time) (clock.DeviceState, error) {
	var state clock.DeviceState
	driftMS := int32(sourceReported.Sub(referenceNow) / time.Millisecond)
	state = clock.DeviceState{
		TenantID: tenant, DeviceID: "source:" + integration, SiteID: site,
		Source: clock.SourceVMS, DriftMs: driftMS,
		Band: policy.Band(driftMS), ReferenceID: referenceID, MeasuredAt: s.now(),
	}
	// A source-system clock is not a device row, so it is stored under a reserved
	// id. The schema's device_clocks is keyed by device, and this is the one entry
	// in it that is not a device: the alternative is a second table for one value,
	// and a site with a fast VMS needs the number in the same place the rules
	// read every other clock.
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "clocksub"},
		func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `
				INSERT INTO device_clocks (tenant_id, device_id, site_id, clock_source, drift_ms, band, reference_id, measured_at)
				VALUES ($1, $2, $3, 'vms', $4, $5, $6, $7)
				ON CONFLICT (tenant_id, device_id) DO UPDATE
				   SET drift_ms = EXCLUDED.drift_ms, band = EXCLUDED.band,
				       reference_id = EXCLUDED.reference_id, measured_at = EXCLUDED.measured_at`,
				tenant, state.DeviceID, site, driftMS, string(state.Band), referenceID, state.MeasuredAt)
			if execErr != nil {
				return fmt.Errorf("clocksub: record source drift: %w", execErr)
			}
			return nil
		})
	return state, err
}

// State reads a device's current clock state.
//
// found is false for a device that has never been measured, which is
// deliberately different from a device measured at exactly zero drift: the first
// has never been checked, the second has been checked and is fine.
func (s *Service) State(ctx context.Context, tenant, deviceID string) (state clock.DeviceState, found bool, err error) {
	var source, band, ref string
	err = s.Pool.QueryRow(ctx, `
		SELECT site_id, clock_source, drift_ms, band, COALESCE(reference_id, ''), measured_at
		  FROM device_clocks WHERE tenant_id = $1 AND device_id = $2`,
		tenant, deviceID).Scan(&state.SiteID, &source, &state.DriftMs, &band, &ref, &state.MeasuredAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return clock.DeviceState{}, false, nil
		}
		return state, false, fmt.Errorf("clocksub: read state: %w", err)
	}
	state.TenantID = tenant
	state.DeviceID = deviceID
	state.Source = clock.Source(source)
	state.Band = clock.Band(band)
	state.ReferenceID = ref
	return state, true, nil
}

// SevereCount is the number of devices past the severe band, which the console
// surfaces and the SLO alerts on.
func (s *Service) SevereCount(ctx context.Context, tenant string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM device_clocks WHERE tenant_id = $1 AND band = 'severe'`,
		tenant).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("clocksub: severe count: %w", err)
	}
	return n, nil
}

// DeviceView is one row of the console's clock-drift view.
type DeviceView struct {
	DeviceID   string    `json:"deviceId"`
	SiteID     string    `json:"siteId"`
	Source     string    `json:"source"`
	DriftMS    int32     `json:"driftMs"`
	Band       string    `json:"band"`
	MeasuredAt time.Time `json:"measuredAt"`
	Excluded   bool      `json:"excludedFromCorroboration"`
	Reason     string    `json:"reason,omitempty"`
}

// View lists the site's device clocks with the corroboration exclusion the
// policy implies, so an operator sees the policy applied rather than having to
// derive it.
func (s *Service) View(ctx context.Context, policy clock.Policy, tenant string, limit int) ([]DeviceView, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT device_id, site_id, clock_source, drift_ms, band, measured_at
		  FROM device_clocks WHERE tenant_id = $1
		 ORDER BY abs(drift_ms) DESC, device_id
		 LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, fmt.Errorf("clocksub: view: %w", err)
	}
	defer rows.Close()

	var out []DeviceView
	for rows.Next() {
		var d DeviceView
		if err := rows.Scan(&d.DeviceID, &d.SiteID, &d.Source, &d.DriftMS, &d.Band, &d.MeasuredAt); err != nil {
			return nil, fmt.Errorf("clocksub: scan view: %w", err)
		}
		if d.Band == string(clock.BandSevere) {
			d.Excluded = true
			d.Reason = fmt.Sprintf("device clock drift %dms is past the site's severe band (>= %dms)",
				d.DriftMS, policy.SevereMs)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
