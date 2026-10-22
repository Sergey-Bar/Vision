package calibration

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/threat"
)

// The local base rate.
//
// # What it is
//
// The share of a rule's incidents AT ONE SITE that the operator called real.
// It is the answer to "how often is this rule right HERE", and it is the one
// input the legacy engine did not have.
//
// # Why it is per site
//
// A rule that is right nine times in ten in a lobby is right two times in ten
// in a loading bay, and a global constant would tell the engine to treat
// both the same. The site is where the base rate lives, and the site's own
// labelled history is where it comes from.
//
// # Smoothing
//
// Laplace: `(tp + α) / (tp + fp + α + β)` with α = β = 1.
//
// Without it, a rule with one true positive and no false positives has a
// prior of 1, and the next incident it raises scores as high as the first
// one did, and the one after that drops to zero. With it, a rule with one
// true positive has a prior of 2/3 and a rule with none has 1/(n+1) —
// neither of them is a claim about the world that the engine should act on.
//
// # Absence
//
// A site with no labelled history has NO prior, and the engine leaves its
// score alone. That is not a default of 0.5: 0.5 is a claim ("half of this
// rule's incidents are real") and the site has made no such claim. The
// difference matters at a fresh install, where the engine must behave like
// the one it replaced.

// The Laplace smoothing constants.
const (
	priorAlpha = 1.0
	priorBeta  = 1.0
)

// Prior is the base rate for one rule at one site.
//
// It is the same shape in the corpus and in the store, because it is the
// same fact: the site's smoothed precision for a rule, and whether the site
// had any labelled history for it at all.
type Prior struct {
	// Rate is the smoothed precision, 0..1.
	Rate float64 `json:"rate"`
	// Known is whether the site had any labelled history for the rule.
	Known bool `json:"known"`
}

// SmoothedPrior is the Laplace-smoothed prior for a count of outcomes.
//
// It is exported because the corpus and the store must agree on it, and two
// implementations of a smoothing formula is one of them wrong.
func SmoothedPrior(truePositives, falsePositives int) Prior {
	tp := float64(truePositives)
	fp := float64(falsePositives)
	total := tp + fp
	if total <= 0 {
		return Prior{Rate: 0, Known: false}
	}
	return Prior{
		Rate:  (tp + priorAlpha) / (tp + fp + priorAlpha + priorBeta),
		Known: true,
	}
}

// Store reads the site's labelled history.
//
// It is the product's implementation of `threat.PriorLookup`: the engine
// asks it for a rule's base rate at a site, and it answers from the
// database.
//
// The history is CACHED with a short TTL, because the scorer asks for a
// prior on every incident it raises and the answer changes only when an
// operator records a disposition.
type Store struct {
	pool   *dbx.Pool
	tenant string
	ttl    time.Duration

	mu      sync.Mutex
	cached  map[string]priorEntry
	fetched time.Time
}

type priorEntry struct {
	prior Prior
}

// NewStore creates the store.
//
// The TTL is how long a fetched history is trusted. It is short enough that
// an operator's disposition changes the next few incidents' scores, and long
// enough that a burst of incidents does not read the database once each.
func NewStore(pool *dbx.Pool, tenant string, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &Store{
		pool:   pool,
		tenant: tenant,
		ttl:    ttl,
		cached: map[string]priorEntry{},
	}
}

// Prior returns the base rate for a rule at a site.
//
// It satisfies `threat.PriorLookup`, which is how the engine takes its
// prior without depending on a database.
func (s *Store) Prior(site, rule string) (float64, bool) {
	p := s.Lookup(context.Background(), site, rule)
	return p.Rate, p.Known
}

// Lookup returns the base rate for a rule at a site, reading the history if
// the cache has gone stale.
func (s *Store) Lookup(ctx context.Context, site, rule string) Prior {
	key := site + "\x00" + rule

	s.mu.Lock()
	if entry, ok := s.cached[key]; ok && time.Since(s.fetched) < s.ttl {
		s.mu.Unlock()
		return entry.prior
	}
	s.mu.Unlock()

	tp, fp, err := s.count(ctx, site, rule)
	if err != nil {
		// A history that cannot be read is an ABSENCE, not a negative
		// observation. The engine falls back to the legacy behaviour, which
		// is the safe direction: an uncalibrated score is the score the
		// product shipped with.
		return Prior{Rate: 0, Known: false}
	}

	prior := SmoothedPrior(tp, fp)

	s.mu.Lock()
	s.cached[key] = priorEntry{prior: prior}
	s.fetched = time.Now()
	s.mu.Unlock()

	return prior
}

// SiteHasHistory reports whether the site has ANY labelled history.
//
// It is the question the autonomy dial asks. The per-rule prior is what the
// engine scores against; this is the SITE-level answer, and it is what
// decides whether the site has earned the level at which the Chief may
// dispose of an incident by itself.
//
// The same outcomes count as in `count`: only `true_positive` and
// `false_positive` are judgements about whether the incident was real. A
// site whose operators have only ever acknowledged or dismissed without
// saying whether it was real has no base rate, and the answer is no.
func (s *Store) SiteHasHistory(ctx context.Context, site string) (bool, error) {
	var tp, fp int
	err := s.pool.For(s.tenant, "incidents").QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE d.outcome = 'true_positive'),
			count(*) FILTER (WHERE d.outcome = 'false_positive')
		FROM dispositions d
		JOIN incidents i
		  ON i.tenant_id = d.tenant_id
		 AND i.incident_id = d.incident_id
		WHERE d.tenant_id = $1
		  AND i.primary_site_id = $2`,
		s.tenant, site).Scan(&tp, &fp)
	if err != nil {
		return false, fmt.Errorf("calibration: read the labelled history for %s: %w", site, err)
	}
	return tp+fp > 0, nil
}

// count reads the labelled outcomes for a rule at a site.
//
// Only `true_positive` and `false_positive` count. The other outcomes are
// not judgements about whether the incident was real, and counting them
// would teach the engine something the operator did not say.
func (s *Store) count(ctx context.Context, site, rule string) (int, int, error) {
	var tp, fp int
	err := s.pool.For(s.tenant, "incidents").QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE d.outcome = 'true_positive'),
			count(*) FILTER (WHERE d.outcome = 'false_positive')
		FROM dispositions d
		JOIN incidents i
		  ON i.tenant_id = d.tenant_id
		 AND i.incident_id = d.incident_id
		WHERE d.tenant_id = $1
		  AND i.primary_site_id = $2
		  AND i.rule_id = $3`,
		s.tenant, site, rule).Scan(&tp, &fp)
	if err != nil {
		return 0, 0, fmt.Errorf("calibration: read the labelled history for %s/%s: %w", site, rule, err)
	}
	return tp, fp, nil
}

// MapPrior is an in-memory prior, for the evaluation and for tests.
//
// It is a `threat.PriorLookup` over a map, and it is how the corpus's
// leave-one-out priors are supplied to the engine.
type MapPrior map[string]Prior

// Prior returns the base rate for a rule at a site.
func (m MapPrior) Prior(site, rule string) (float64, bool) {
	p, ok := m[priorKey(site, rule)]
	if !ok {
		return 0, false
	}
	return p.Rate, p.Known
}

// priorKey is the key a prior is stored under.
func priorKey(site, rule string) string { return site + "\x00" + rule }

// Compile-time proof that the two implementations agree on the interface.
var _ threat.PriorLookup = (*Store)(nil)
var _ threat.PriorLookup = MapPrior(nil)
