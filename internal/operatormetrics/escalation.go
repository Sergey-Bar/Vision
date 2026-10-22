package operatormetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sergey-Bar/vision/internal/observations"
)

// The AI's false-escalation rate.
//
// # What it is
//
// The share of the model's ESCALATIONS that the operator later called false.
//
// The escalations are the model's restricted verdicts: the model saying
// something is here. The false ones are those the operator dispositioned
// `false_positive`.
//
// # The denominators, and why each one is what it is
//
// The denominator is the escalations the operator JUDGED.
//
// Not the escalations there were: an escalation nobody judged is neither a true
// escalation nor a false one, and counting it in either direction is how a
// profile looks better than it is. And not the observations at all: a degraded
// observation and an insufficient-evidence one carry no verdict, so they are
// not escalations and they are not a denominator.
//
// # Where the numbers come from
//
// The observations and the dispositions, over the window. The evaluation
// package computes the same shape from a committed JSON corpus, and the corpus
// is a FIXTURE: this is the production path, and the fixture stays as the
// thing that proves the arithmetic.
//
// The model's verdict is the profile's own score field, read from the
// observation's structured output, and the same registry the evaluation
// package reads: a field one profile declares is not a field every profile
// declares, and a hard-coded name would score every profile against one
// profile's claim.

// AIFalseEscalation is the companion's measurement.
type AIFalseEscalation struct {
	// Value is the share of the escalations that were false, and it is nil
	// when there were no judged escalations.
	Value *float64 `json:"value"`
	// N is the number of judged escalations the rate is over.
	N int64 `json:"n"`
	// CI is the Wilson interval on the rate.
	CI *Interval `json:"confidence_interval"`
	// Escalations is how many times the model said something was here.
	Escalations int64 `json:"escalations"`
	// Judged is how many of those an operator answered.
	Judged int64 `json:"judged"`
	// Undispositioned is how many observations nobody judged.
	//
	// It is reported and never folded into a denominator: it is the count of
	// cases the product has no evidence about, and a rate that included them
	// would be a rate about silence.
	Undispositioned int64 `json:"undispositioned"`
	// Degraded is how many observations the AI plane could not answer.
	Degraded int64 `json:"degraded"`
	// Insufficient is how many the model said it could not tell.
	Insufficient int64 `json:"insufficient"`
}

// AIFalseEscalationRate computes the companion over the window.
//
// It returns the zero measurement when the window holds no judged escalation:
// the companion is then reported as NOT MEASURED, which is a different answer
// from a rate of zero.
func (r *Reader) AIFalseEscalationRate(ctx context.Context, tenant string, w Window) (AIFalseEscalation, error) {
	if r.Pool == nil {
		return AIFalseEscalation{}, ErrNoPool
	}
	if tenant == "" {
		return AIFalseEscalation{}, fmt.Errorf("operatormetrics: a rate with no tenant is a rate about everybody")
	}
	if !w.To.After(w.From) {
		return AIFalseEscalation{}, fmt.Errorf("operatormetrics: the window [%s, %s) is empty",
			w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	}

	out := AIFalseEscalation{}

	// The observations in the window, with the operator's verdict where there
	// is one.
	rows, err := r.Pool.WithSites(tenant, "analytics", r.Sites).Query(ctx, `
		SELECT o.observation_id, o.profile_id, o.structured_output,
		       o.degraded_reason, o.insufficient_evidence,
		       d.outcome
		  FROM ai_observations o
		  LEFT JOIN dispositions d
		    ON d.tenant_id = o.tenant_id
		   AND d.related_observation_id = o.observation_id
		 WHERE o.tenant_id = $1
		   AND o.created_at >= $2 AND o.created_at < $3`,
		tenant, w.From.UTC(), w.To.UTC())
	if err != nil {
		return out, fmt.Errorf("operatormetrics: read the observations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			observationID, profileID string
			structured               []byte
			degradedReason           string
			insufficient             bool
			outcome                  *string
		)
		if err := rows.Scan(&observationID, &profileID, &structured, &degradedReason, &insufficient, &outcome); err != nil {
			return out, fmt.Errorf("operatormetrics: read the observations scan: %w", err)
		}
		_ = observationID

		// A DEGRADED observation and an INSUFFICIENT one carry no verdict, so
		// they are not escalations and they are not a denominator.
		if degradedReason != "" && degradedReason != "none" {
			out.Degraded++
			continue
		}
		if insufficient {
			out.Insufficient++
			continue
		}
		if outcome == nil {
			out.Undispositioned++
			continue
		}

		// The model's verdict, from the PROFILE's own field.
		field, known := observations.ProfileScoreField(profileID)
		if !known || field == "" {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(structured, &fields); err != nil {
			// A malformed structured output is not a verdict and not a
			// false positive. It is counted as neither.
			continue
		}
		raw, present := fields[field]
		if !present {
			continue
		}
		verdict, ok := raw.(bool)
		if !ok || !verdict {
			// Not an escalation: the model said nothing was restricted.
			continue
		}

		out.Escalations++
		out.Judged++
		if *outcome == "false_positive" {
			out.N++
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("operatormetrics: read the observations rows: %w", err)
	}

	if out.Judged > 0 {
		v := float64(out.N) / float64(out.Judged)
		out.Value = &v
		lower, upper := WilsonInterval(out.N, out.Judged, 1.959963984540054)
		out.CI = &Interval{Method: "wilson", Lower: lower, Upper: upper}
	}
	return out, nil
}
