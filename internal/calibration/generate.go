package calibration

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/threat"
)

// The corpus generator.
//
// It reads the product's OWN labelled history and writes a corpus in the
// same shape as the committed one, so a site with real history measures the
// engine against its own labels rather than against a fixture.
//
// # Where the features come from
//
// The features are read out of the incident's stored reason chain, because
// that is what the engine read when it scored the incident, and the corpus
// has to hold the same facts the engine saw. A corpus that recomputed the
// features from the raw events would hold slightly different numbers from
// the ones the decision was made on, and the measurement would be of a
// different engine.
//
// The chain's steps carry the facts in prose, and the parsers below read
// that prose. A step that cannot be read is a case the generator SKIPS, and
// the skip is counted and reported: a corpus that silently dropped the
// cases it could not read would be a corpus that measured the easy half.

// corpusProvenance is the provenance a generated corpus states.
//
// It says where the cases came from, in the corpus's own file, because a
// corpus with no stated provenance reads as somebody's real history.
const corpusProvenance = "Generated from this installation's own labelled history: the operator's " +
	"dispositions joined to the incidents they were recorded against, with the features the engine " +
	"read out of the incident's stored reason chain. The priors are leave-one-out: each case carries " +
	"the site's smoothed precision computed without this case's own label."

// GenerateCorpus reads the tenant's labelled history and writes a corpus.
//
// The tenant is the one the install is configured with: the product is
// single-tenant by construction, and a corpus that mixed tenants would be a
// corpus that measured nothing.
func GenerateCorpus(ctx context.Context, pool *dbx.Pool, tenant string) (Corpus, int, error) {
	return GenerateCorpusInWindow(ctx, pool, tenant, time.Time{}, time.Time{})
}

// GenerateCorpusInWindow reads the tenant's labelled history IN THE WINDOW.
//
// A zero bound means "no bound on that side", so the whole history is the
// cold install's case and the one the committed corpus stands in for.
//
// The window exists because a report has to be COMPARABLE. A corpus of
// everything the install has ever seen changes every time an operator labels
// one more incident, and two such reports cannot be compared. A corpus over a
// window is a measurement of a period, and two periods can be compared.
func GenerateCorpusInWindow(ctx context.Context, pool *dbx.Pool, tenant string, from, to time.Time) (Corpus, int, error) {
	var c Corpus
	c.ContractVersion = CorpusVersion
	c.Provenance = corpusProvenance

	rows, err := pool.For(tenant, "incidents").Query(ctx, `
		SELECT i.incident_id, i.primary_site_id, i.rule_id, d.outcome, i.threat_reason_chain
		FROM dispositions d
		JOIN incidents i
		  ON i.tenant_id = d.tenant_id
		 AND i.incident_id = d.incident_id
		WHERE d.tenant_id = $1
		  AND d.outcome IN ('true_positive', 'false_positive')
		  AND ($2::timestamptz IS NULL OR i.created_at >= $2)
		  AND ($3::timestamptz IS NULL OR i.created_at <= $3)
		ORDER BY i.created_at`, tenant, nullTime(from), nullTime(to))
	if err != nil {
		return c, 0, fmt.Errorf("calibration: read the labelled history: %w", err)
	}
	defer rows.Close()

	skipped := 0
	for rows.Next() {
		var id, site, rule, outcome string
		var chain []byte
		if err := rows.Scan(&id, &site, &rule, &outcome, &chain); err != nil {
			return c, skipped, fmt.Errorf("calibration: scan the labelled history: %w", err)
		}
		features, ok := featuresFromChain(chain)
		if !ok {
			skipped++
			continue
		}
		c.Cases = append(c.Cases, Case{
			CaseID:   id,
			Site:     site,
			Rule:     rule,
			Label:    Label(outcome),
			Features: features,
		})
	}
	if err := rows.Err(); err != nil {
		return c, skipped, fmt.Errorf("calibration: read the labelled history: %w", err)
	}
	if len(c.Cases) == 0 {
		return c, skipped, fmt.Errorf("calibration: this installation has no labelled history, " +
			"so there is no corpus to generate")
	}

	c = ComputePriors(c)
	c = BuildExpectations(c)
	return c, skipped, nil
}

// The chain's prose, as patterns.
//
// They are anchored on the wording the calibrated engine writes, and they
// are the reason the engine's step wording is part of a contract: a change
// to the wording changes what a corpus can be generated from.
var (
	presenceRe = regexp.MustCompile(`^presence in a (\S+) zone, severity (\S+), ` +
		`([0-9.]+)s of a ([0-9.]+)s threshold$`)
	corroborationRe = regexp.MustCompile(`^(\d+) devices saw it$`)
)

// featuresFromChain reads the features out of a stored reason chain.
//
// It returns false when the chain does not carry them, which is a case the
// generator skips: the chain is the engine's own record, and a chain written
// by the legacy engine carries neither the presence nor the device count.
func featuresFromChain(raw []byte) (Features, bool) {
	var f Features
	if len(raw) == 0 {
		return f, false
	}
	var steps []threat.ReasonStep
	if err := json.Unmarshal(raw, &steps); err != nil {
		return f, false
	}
	for _, s := range steps {
		switch s.Key {
		case "presence":
			m := presenceRe.FindStringSubmatch(s.Evidence)
			if m == nil {
				return f, false
			}
			f.AnomalyType = "presence"
			f.ZoneType = m[1]
			f.Severity = m[2]
			f.PresentSeconds, _ = strconv.ParseFloat(m[3], 64)
			f.ThresholdSeconds, _ = strconv.ParseFloat(m[4], 64)
		case "corroboration":
			m := corroborationRe.FindStringSubmatch(s.Evidence)
			if m == nil {
				continue
			}
			f.Devices, _ = strconv.Atoi(m[1])
		}
	}
	if f.ZoneType == "" || f.ThresholdSeconds <= 0 {
		return f, false
	}
	// A case with no corroboration step saw the thing once.
	if f.Devices == 0 {
		f.Devices = 1
	}
	return f, true
}

// Describe renders a corpus's shape in one line, for a log.
func Describe(c Corpus) string {
	sites := map[string]bool{}
	rules := map[string]bool{}
	tp, fp := 0, 0
	for _, cs := range c.Cases {
		sites[cs.Site] = true
		rules[cs.Rule] = true
		if cs.Label == LabelTruePositive {
			tp++
		} else {
			fp++
		}
	}
	return fmt.Sprintf("%d cases across %d site(s) and %d rule(s): %d true positives, %d false positives",
		len(c.Cases), len(sites), len(rules), tp, fp)
}

// nullTime maps the zero time onto NULL, so a bound that is not set does not
// filter.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
