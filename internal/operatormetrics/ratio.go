package operatormetrics

import (
	"context"
	"fmt"
	"time"
)

// M1 gate 5's ratio.
//
// # The gate
//
// "The 14-day run records events ingested → canonical events → incidents
// raised, per camera, and the ratio is published in the release notes."
//
// The three legs and the ratio, per camera, over a window.
//
// # What the rollup can and cannot answer
//
// The rollup carries `canonical_events` per DEVICE and `incidents_raised` per
// RULE. The ratio is per camera, and the two are at different scopes, so the
// rollup cannot answer it: a camera's incidents are counted by the rule that
// raised them, and there is no per-camera incidents row.
//
// So the ratio is computed from the SOURCE tables, in one query, and the
// response says so. A gate artefact is a Postgres computation over the tables
// the product already writes, and a query over `canonical_events` and
// `incidents` is exactly that; the rollup is an optimisation for the dashboards
// that ask for the same window every minute, and this is a fortnight's
// arithmetic asked for once.
//
// # The third leg
//
// "Events ingested" is the inbox's accepted count, and the inbox is per
// INTEGRATION: it has no device, because the device is resolved later, by the
// normalizer. So the third leg is a TENANT number and the per-camera rows
// carry two of the three.
//
// That is stated in the response rather than left for the reader to discover,
// because a gate that says "per camera" and then gives a tenant number for one
// of its three legs is a gate that reads as complete and is not.

// IncidentRateRow is one camera's row, or the total's.
//
// The totals are the same shape as the rows, so a reader sums rows and gets
// the total rather than having to remember which is which.
type IncidentRateRow struct {
	// SiteID is the site the camera is at, and empty for the total.
	SiteID string `json:"site_id,omitempty"`
	// DeviceID is the camera, and empty for the total.
	DeviceID string `json:"device_id,omitempty"`
	// CanonicalEvents is how many canonical events the camera produced.
	CanonicalEvents int64 `json:"canonical_events"`
	// IncidentsRaised is how many incidents the camera raised.
	//
	// An incident is attributed to the camera whose event TRIGGERED it, which
	// is the only attribution the data supports: an incident may correlate
	// several cameras' events, and the one that triggered it is the one the
	// rule fired on.
	IncidentsRaised int64 `json:"incidents_raised"`
	// Rate is the incidents per thousand canonical events, and it is nil when
	// the camera produced no canonical events.
	Rate *float64 `json:"rate"`
	// CI is the Wilson interval on the rate, and it is nil when there is no
	// rate.
	CI *Interval `json:"confidence_interval"`
}

// IncidentRateReport is M1 gate 5's ratio over a window.
type IncidentRateReport struct {
	// Window is the period the numbers cover.
	Window Window `json:"window"`
	// Source says where the numbers came from: `postgres`.
	Source string `json:"source"`
	// ComputedAt is when the ratio was computed.
	ComputedAt time.Time `json:"computed_at"`
	// Rows are the cameras, in a stable order.
	Rows []IncidentRateRow `json:"rows"`
	// Total is every camera together.
	Total IncidentRateRow `json:"total"`
	// Note carries the two things a reader needs to know and would otherwise
	// have to guess.
	Note string `json:"note"`
}

// IncidentRate computes M1 gate 5's ratio over the window.
//
// The window is the one the gate names: a fortnight by default, because the
// gate says a 14-day run, and a fortnight is long enough for a camera's
// behaviour to be a pattern rather than an afternoon.
func (r *Reader) IncidentRate(ctx context.Context, tenant string, w Window) (IncidentRateReport, error) {
	if r.Pool == nil {
		return IncidentRateReport{}, ErrNoPool
	}
	if tenant == "" {
		return IncidentRateReport{}, fmt.Errorf("operatormetrics: a ratio with no tenant is a ratio " +
			"about everybody")
	}
	if !w.To.After(w.From) {
		return IncidentRateReport{}, fmt.Errorf("operatormetrics: the window [%s, %s) is empty",
			w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	}

	out := IncidentRateReport{
		Window:     w,
		Source:     "postgres",
		ComputedAt: r.now(),
		Rows:       []IncidentRateRow{},
		Note: "The ratio is incidents raised per 1,000 canonical events, per camera. The third leg of " +
			"M1 gate 5 -- events INGESTED -- is a tenant number and is not in this response: the " +
			"inbox is per integration and a camera's events are resolved later, by the normalizer. " +
			"An incident is attributed to the camera whose event triggered it.",
	}

	rows, err := r.Pool.WithSites(tenant, "analytics", r.Sites).Query(ctx, `
		WITH events AS (
		    SELECT ce.device_id, ce.site_id, count(*)::bigint AS canonical_events
		      FROM canonical_events ce
		     WHERE ce.tenant_id = $1
		       AND ce.normalized_at >= $2 AND ce.normalized_at < $3
		     GROUP BY ce.device_id, ce.site_id
		), raised AS (
		    SELECT ce.device_id, count(DISTINCT i.incident_id)::bigint AS incidents_raised
		      FROM incident_events ie
		      JOIN incidents i
		        ON i.tenant_id = ie.tenant_id
		       AND i.incident_id = ie.incident_id
		      JOIN canonical_events ce
		        ON ce.tenant_id = ie.tenant_id
		       AND ce.event_id = ie.event_id
		     WHERE ie.tenant_id = $1
		       AND ie.role = 'trigger'
		       AND i.created_at >= $2 AND i.created_at < $3
		     GROUP BY ce.device_id
		)
		SELECT coalesce(e.device_id, r.device_id), coalesce(e.site_id, ''),
		       coalesce(e.canonical_events, 0), coalesce(r.incidents_raised, 0)
		  FROM events e
		  FULL JOIN raised r ON r.device_id = e.device_id
		 ORDER BY 2, 1`,
		tenant, w.From.UTC(), w.To.UTC())
	if err != nil {
		return out, fmt.Errorf("operatormetrics: read the incident rate: %w", err)
	}
	defer rows.Close()

	var totalEvents, totalIncidents int64
	for rows.Next() {
		var (
			deviceID, siteID       string
			canonical, raisedCount int64
		)
		if err := rows.Scan(&deviceID, &siteID, &canonical, &raisedCount); err != nil {
			return out, fmt.Errorf("operatormetrics: read the incident rate scan: %w", err)
		}
		row := IncidentRateRow{
			SiteID:          siteID,
			DeviceID:        deviceID,
			CanonicalEvents: canonical,
			IncidentsRaised: raisedCount,
		}
		row.Rate, row.CI = ratePerThousand(raisedCount, canonical)
		totalEvents += canonical
		totalIncidents += raisedCount
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("operatormetrics: read the incident rate rows: %w", err)
	}

	out.Total = IncidentRateRow{CanonicalEvents: totalEvents, IncidentsRaised: totalIncidents}
	out.Total.Rate, out.Total.CI = ratePerThousand(totalIncidents, totalEvents)
	return out, nil
}

// ratePerThousand is a count per thousand, with its interval.
//
// A zero denominator is a NIL rate and not a zero: a camera that produced no
// canonical events has no rate, and a rate of zero would read as "this camera
// never raised an incident", which is a different statement.
func ratePerThousand(numerator, denominator int64) (*float64, *Interval) {
	if denominator <= 0 {
		return nil, nil
	}
	v := float64(numerator) / float64(denominator) * 1000
	lower, upper := WilsonInterval(numerator, denominator, 1.959963984540054)
	return &v, &Interval{Method: "wilson", Lower: lower * 1000, Upper: upper * 1000}
}
