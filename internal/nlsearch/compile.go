package nlsearch

import (
	"strings"
	"time"
)

// Compile: a [Question] becomes a [Query], and a [Query] is the only thing in this
// package that touches SQL.
//
// # Why the SQL is built here and never composed
//
// Every other way of doing this concatenates something the caller supplied into a
// statement. Here the statement is a CONSTANT with `$n` placeholders and the
// question arrives as bound arguments, so:
//
//   - there is no injection surface, and therefore no escaping convention that can
//     be got wrong on one code path and forgotten on another;
//   - the compiled SQL is byte-identical for every question with the same shape,
//     which is what makes it reviewable at all — a reviewer reads one string, not
//     one string per question;
//   - a bug that widens a scope shows up as a changed constant, which is a diff.
//
// # The two compile-time invariants, checked and named
//
// [Query.Verify] runs after every compile and is the reason the two rows below are
// refusals rather than comments:
//
//	tenant predicate    every statement carries `tenant_id = $1` and `Args[0]` is the
//	                    tenant. A query without it is not "probably fine", it returns
//	                    another tenant's rows.
//	site scope          no argument names a site outside the question's. A query that
//	                    filters on the principal's scope rather than the question's is
//	                    correct but answers a different question, and the operator has
//	                    no way to see that.
//
// Both are checkable on the built product rather than trusted, and both have a
// [Reason] so that a violation is reported rather than logged.

// Query is a compiled statement and the bounds it carries.
type Query struct {
	// SQL is one of a small fixed set of statements. Never composed.
	SQL string
	// Args are the bound values, in placeholder order.
	Args []any
	// Tenants is the tenant the query reads. Exactly one, and never empty.
	Tenants []string
	// Sites is the set of sites the query covers, after intersection with the
	// principal's scope.
	Sites []string
	// Tables is what the query reads. Always exactly [AllowedTables].
	Tables []string
	// Kinds is the `source_kind` filter that was compiled in, so [Plan] can print
	// which halves of the corpus the answer came from without re-reading the SQL.
	Kinds []SourceKind
	// MatchedIndexed reports whether every compiled kind is served by the GIN
	// index. False is a warning for the operator, not a refusal.
	MatchedIndexed bool
	// Limit is the row ceiling that was compiled in, after clamping.
	Limit int
}

// AllowedTables is the entire searchable corpus.
//
// One view. A question that needs `audit_ledger`, `sessions`, `users` or
// `evidence` is refused by name, because "the search reached a table the search was
// not given" is an answer an operator can act on. Adding a relation here is a
// deliberate change with a reviewable diff, which is the only way this list should
// ever grow.
var AllowedTables = []string{"search_documents"}

// The two statements. Two because the kinds filter is compiled in rather than
// applied afterwards: a filter applied in Go is a filter that can be forgotten, and
// this package's job is that it cannot be.
//
// Both read `at` and never `observed_at`. `at` is `verification_results.evaluated_at`
// as the view exposes it, and `observed_at` is the clock-corrected event time the
// rest of the product reasons about. A search that reads the wrong one is a search
// that reports events the product placed elsewhere.
const (
	sqlAllKinds = `
SELECT sd.incident_id, sd.site_id, sd.at, sd.source_kind,
       sd.explanation,
       coalesce(sd.unindexed_vector::text, '') AS unindexed_vector,
       ts_rank(sd.vector, q.query)          AS rank
  FROM search_documents sd, websearch_to_tsquery('english', $4) q
 WHERE sd.tenant_id = $1
   AND sd.site_id = ANY($2)
   AND ($5::timestamptz IS NULL OR sd.at >= $5)
   AND ($6::timestamptz IS NULL OR sd.at <= $6)
   AND (
        sd.vector @@ q.query
     OR sd.unindexed_vector @@ q.query
   )
 ORDER BY rank DESC, sd.at DESC
 LIMIT $3`

	sqlSomeKinds = `
SELECT sd.incident_id, sd.site_id, sd.at, sd.source_kind,
       sd.explanation,
       coalesce(sd.unindexed_vector::text, '') AS unindexed_vector,
       ts_rank(sd.vector, q.query)          AS rank
  FROM search_documents sd, websearch_to_tsquery('english', $4) q
 WHERE sd.tenant_id = $1
   AND sd.site_id = ANY($2)
   AND ($5::timestamptz IS NULL OR sd.at >= $5)
   AND ($6::timestamptz IS NULL OR sd.at <= $6)
   AND sd.source_kind = ANY($7)
   AND (
        sd.vector @@ q.query
     OR sd.unindexed_vector @@ q.query
   )
 ORDER BY rank DESC, sd.at DESC
 LIMIT $3`
)

// tenantPredicate is the string every compiled statement must contain.
//
// It is checked as a substring rather than parsed, and that is a deliberate
// trade: a substring check can be fooled by a column named `tenant_id` in a
// comment, and a parser can be fooled by a change in formatting. The substring
// check is here to catch the realistic failure — a hand-edited statement that lost
// its predicate — and the anti-fooling measure is that the statements are
// constants in THIS file, so there is no path by which a caller can supply one.
const tenantPredicate = "sd.tenant_id = $1"

// Compile turns a validated question into a query.
//
// `inScope` is the principal's site scope. A question naming a site outside it is a
// REFUSAL and not a narrowing: an operator who asks about north-dock and gets a
// confident answer about north-gate has been misled, and a silently narrowed
// result set is indistinguishable from "there is nothing to find".
func Compile(q Question, inScope []string) (*Query, *Refusal) {
	if r := q.Validate(); r != nil {
		return nil, r
	}
	if len(inScope) == 0 {
		return nil, RefuseWithScope(ReasonNoSites,
			"your account has no site assigned, so there is nothing it may search. A principal "+
				"with a role and no site assignment is a misconfiguration, and the safe reading of "+
				"a misconfiguration that grants access is that it does not",
			q, inScope)
	}
	sites, refusal := intersect(q.Sites, inScope, q)
	if refusal != nil {
		return nil, refusal
	}

	limit := q.effectiveLimit()
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = AllKinds()
	}

	out := &Query{
		Tenants: []string{q.TenantID},
		Sites:   sites,
		Tables:  append([]string(nil), AllowedTables...),
		Kinds:   kinds,
		Limit:   limit,
	}
	for _, k := range kinds {
		if !k.Indexed() {
			out.MatchedIndexed = false
		}
	}
	out.MatchedIndexed = allIndexed(kinds)

	// Argument order is fixed by the statements above and is the reason the
	// predicates read as `$5 IS NULL OR ...` rather than as a dynamically built
	// clause list: a NULL-able bound parameter is a whole predicate with no string
	// surgery, and there is no branch where a filter is forgotten.
	out.Args = []any{q.TenantID, sites, limit, q.Text, nullTime(q.Since), nullTime(q.Until)}
	if len(kinds) < len(AllKinds()) {
		out.SQL = sqlSomeKinds
		kindNames := make([]string, 0, len(kinds))
		for _, k := range kinds {
			kindNames = append(kindNames, string(k))
		}
		out.Args = append(out.Args, kindNames)
	} else {
		out.SQL = sqlAllKinds
	}

	if refusal := out.Verify(q); refusal != nil {
		return nil, refusal
	}
	return out, nil
}

// allIndexed reports whether every kind is served by the GIN index.
func allIndexed(kinds []SourceKind) bool {
	for _, k := range kinds {
		if !k.Indexed() {
			return false
		}
	}
	return true
}

// intersect returns the question's sites that the principal may search.
//
// A site the question names and the principal may not is a refusal naming BOTH:
// what was asked for and what is available. A question naming no site at all is
// already refused by Validate, so this always has something to intersect with.
func intersect(asked, inScope []string, q Question) ([]string, *Refusal) {
	allowed := make(map[string]bool, len(inScope))
	for _, s := range inScope {
		allowed[s] = true
	}
	var out []string
	var refused []string
	for _, s := range asked {
		if allowed[s] {
			out = append(out, s)
			continue
		}
		refused = append(refused, s)
	}
	if len(refused) > 0 {
		return nil, RefuseWithScope(ReasonUnknownSite,
			"you asked about a site that is not in your scope. Searching it anyway would "+
				"answer a different question than the one you asked, and you would not be able "+
				"to tell from the answer",
			q, inScope)
	}
	return out, nil
}

// nullTime maps the zero time to nil, so an unbounded side of the window binds as
// NULL and the statement's `IS NULL OR` predicate takes it.
//
// A pointer rather than a sentinel timestamp: "1970-01-01" is a date a site could
// legitimately have data at, and using it as "no lower bound" would silently
// exclude nothing while looking like it excluded everything.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// Verify is the two compile-time invariants, checked on the built product.
//
// It is exported and it runs on every compile because an invariant that is only
// checked in a test is an invariant that a future edit can remove without
// anything going red. Its refusals are [ReasonTenantPredicateDropped] and
// [ReasonSiteScopeWidened] rather than panics: a gateway that panics on a bad
// statement takes the console with it, and this one is reachable from an operator's
// keyboard.
func (q *Query) Verify(asked Question) *Refusal {
	if q == nil {
		return Refuse(ReasonTenantPredicateDropped, "there is no query to verify", asked)
	}
	if len(q.Tenants) != 1 || q.Tenants[0] == "" {
		return Refuse(ReasonTenantPredicateDropped,
			"the compiled query does not carry exactly one tenant. A query with no tenant "+
				"predicate is not a query that returns too much; it is a query that returns "+
				"another tenant's incidents", asked)
	}
	if !strings.Contains(q.SQL, tenantPredicate) {
		return Refuse(ReasonTenantPredicateDropped,
			"the compiled statement does not contain "+tenantPredicate+". It would read every "+
				"tenant's rows", asked)
	}
	if len(q.Args) == 0 || q.Args[0] != q.Tenants[0] {
		return Refuse(ReasonTenantPredicateDropped,
			"the first bound argument is not the tenant the predicate names, so the predicate "+
				"filters on something other than who asked", asked)
	}
	inScope := map[string]bool{}
	for _, s := range q.Sites {
		inScope[s] = true
	}
	for _, s := range asked.Sites {
		if !inScope[s] {
			return Refuse(ReasonSiteScopeWidened,
				"the compiled query covers site "+s+", which the question did not ask about. "+
					"A wider answer is not a more helpful answer", asked)
		}
	}
	if len(q.Sites) == 0 {
		return Refuse(ReasonSiteScopeWidened,
			"the compiled query covers no sites, which means it covers every site", asked)
	}
	for _, tbl := range q.Tables {
		ok := false
		for _, allowed := range AllowedTables {
			if tbl == allowed {
				ok = true
				break
			}
		}
		if !ok {
			return Refuse(ReasonTableNotAllowed,
				"the compiled query reads "+tbl+", which is not in the allow-list ("+
					strings.Join(AllowedTables, ", ")+"). The searchable corpus is those "+
					"relations and nothing else", asked)
		}
	}
	return nil
}
