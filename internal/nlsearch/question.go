// Package nlsearch is the natural-language search gateway: a question, a query
// that can be read before it runs, and a set of refusals that are specific enough
// to act on.
//
// # What this is for
//
// An operator's question — "what did the reviewer say about the loading bay last
// night" — has to become a query over `search_documents`, and the space between
// those two things is where a security product leaks. The usual approach is to hand
// the question to a model and hand the model's answer to the database, and every
// defence is then a filter on what the model produced. This package inverts it:
//
//	QUESTION --(Compile)--> QUERY --(Plan)--> APPROVED --(Execute)--> ROWS
//	                  |                        |
//	                  +------(Refusal)--------+
//
// The model, if one is used at all, proposes a [Question]. It never writes SQL and
// it never names a table. Everything downstream of a Question is built by this
// package, and the three refusals below are the boundaries of what it will build.
//
// # The one structural guarantee
//
// **Operator text is never interpolated into SQL.** [Question.Text] is either
// matched by the deterministic parser in `Parse` or carried into the `tsquery`
// parameter as a bound argument. There is no code path that concatenates a
// question into a statement, so there is no injection surface to defend and no
// escaping convention to get wrong.
//
// # The four refusals, and why they are the four
//
// A search feature over tenant data has four ways to be wrong, and each is a
// refusal here rather than a filter:
//
//  1. WIDENING THE SITE SCOPE. A question about one site that reads another is the
//     incident that ends a contract. [Question.Sites] is an upper bound, never a
//     suggestion: the compiled query carries the intersection of what was asked and
//     what the principal may see, and an empty intersection is a refusal.
//  2. DROPPING THE TENANT PREDICATE. Every compiled statement carries
//     `tenant_id = $1` and nothing else can produce one that does not. This is the
//     refusal that would be invisible if it were left to review — a query that is
//     correct except for one missing predicate returns a plausible wrong answer
//     rather than an error.
//  3. A TABLE NOT IN THE ALLOW-LIST. The searchable corpus is one view. A question
//     that needs `audit_ledger` or `sessions` is refused by name, because "the
//     search reached a table the search was not given" is a sentence an operator can
//     act on and "permission denied" is not.
//  4. A HOST THAT IS NOT THE COMMITTED ONE. See [Host] — the destination of a
//     request is a build artefact, not a deployment choice, and a search gateway
//     that can be pointed at a new host by editing an environment file is a data
//     exfiltration path with a config key.
//
// # What it is not
//
// It is not a chat interface, and it does not summarise. It returns rows an operator
// can read and cite, and the citation points at an `incident_id` — which is the only
// thing in this product that means anything.
package nlsearch

import (
	"fmt"
	"strings"
	"time"
)

// SourceKind is which column of the corpus an answer may come from.
//
// A closed set, and the reason is `search_documents` itself: `vector` is the
// INDEXED half and `unindexed_vector` is the review note and the model output,
// which are matched with `@@` but not served by an index. Letting an operator ask
// for one or the other is a performance question; letting them ask for NEITHER is
// not a question.
type SourceKind string

// The three kinds, matching the view's `source_kind` column.
const (
	// KindDeterministic is the rule's own explanation. Indexed.
	KindDeterministic SourceKind = "deterministic"
	// KindReviewer is what a human wrote in a disposition. Matched, not indexed.
	KindReviewer SourceKind = "reviewer"
	// KindModel is what an AI observation's structured output said. Matched, not
	// indexed.
	KindModel SourceKind = "model"
)

// AllKinds is every kind, in the order a reader would list them.
func AllKinds() []SourceKind {
	return []SourceKind{KindDeterministic, KindReviewer, KindModel}
}

// ValidKind reports whether a string names a kind.
//
// A named error rather than a bool, because the caller has to say which kind it did
// not recognise and a bool makes that a second lookup that can disagree with the
// first.
func ValidKind(s string) (SourceKind, error) {
	for _, k := range AllKinds() {
		if string(k) == s {
			return k, nil
		}
	}
	names := make([]string, 0, 3)
	for _, k := range AllKinds() {
		names = append(names, string(k))
	}
	return "", fmt.Errorf("%q is not a source kind. These are the three: %s. A fourth would be "+
		"a column in the corpus this build cannot see, and a question answered from a column "+
		"nobody checked is a question with no citation", s, strings.Join(names, ", "))
}

// Indexed reports whether this kind is served by the GIN index.
//
// False for two of the three, and the reason a question naming them is a warning
// rather than a refusal: the answer is correct, the search is a sequential scan,
// and an operator who asks a model-output question on a large corpus deserves to
// know that before they wait.
func (k SourceKind) Indexed() bool { return k == KindDeterministic }

// Question is a typed, structured request. Built by a person, or proposed by a
// model and then typed by a person — which is the whole trust boundary.
type Question struct {
	// TenantID is the principal's tenant. Not a filter the caller chooses: the
	// compiled query's tenant predicate comes from here and nothing else, so a
	// question cannot widen it.
	TenantID string
	// Sites is the set of sites the question is ABOUT. It is an upper bound on the
	// answer and it is intersected with the principal's scope before the query is
	// built. Naming a site the principal cannot see is a refusal, not a silent
	// narrowing — an operator who asks about a site and gets a confident answer
	// about a different one has been misled, and the narrowing would be invisible.
	Sites []string
	// Kinds is the `source_kind` filter, empty meaning all three.
	Kinds []SourceKind
	// Since and Until bound `at`, and are UTC. Zero means unbounded on that side.
	// A time-window predicate reads `at` because that is the column
	// `search_documents` exposes; it never reads `observed_at`, which is what the
	// rest of the product corrects for clock drift.
	Since time.Time
	Until time.Time
	// Text is the operator's words, bound as a parameter and never interpolated.
	Text string
	// Limit is the row ceiling. Bounded here rather than trusted from the caller,
	// because an unbounded search over a corpus a customer owns is how a search box
	// becomes a data export.
	Limit int
}

// The limits, named.
//
// [MaxLimit] is the ceiling an operator may ask for; [DefaultLimit] is what a
// question that did not specify one gets. Both are constants rather than
// configuration because a configurable ceiling is a ceiling somebody will turn up.
const (
	// MaxLimit is the most rows one question may return.
	MaxLimit = 200
	// DefaultLimit is what a question with no limit gets.
	DefaultLimit = 20
)

// Validate is the question's own check, before any SQL exists.
//
// It runs first so that a malformed question is refused in the operator's terms
// rather than in the database's: "you asked about a site you cannot see" is an
// answer and "relation does not exist" is a puzzle.
func (q Question) Validate() *Refusal {
	if strings.TrimSpace(q.TenantID) == "" {
		return Refuse(ReasonNoTenant, "a question with no tenant is a question about every tenant", q)
	}
	if len(q.Sites) == 0 {
		// Not an empty result. An empty result would be indistinguishable from "there
		// is nothing to find", and the two send an operator to opposite places.
		return Refuse(ReasonNoSites, "a question with no sites is not a narrow question, it is a "+
			"search of everything this tenant has, which is the shape of an export", q)
	}
	for _, s := range q.Sites {
		if strings.TrimSpace(s) == "" {
			return Refuse(ReasonNoSites, "a question naming an empty site is a question naming no site, "+
				"and the difference is not visible in the answer", q)
		}
	}
	for _, k := range q.Kinds {
		if _, err := ValidKind(string(k)); err != nil {
			return Refuse(ReasonUnknownKind, err.Error(), q)
		}
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && q.Until.Before(q.Since) {
		return Refuse(ReasonInvertedWindow, fmt.Sprintf(
			"the window ends at %s, before it starts at %s. A window that ends before it starts "+
				"matches nothing, and 'no results' is what an operator would read",
			q.Until.UTC().Format(time.RFC3339), q.Since.UTC().Format(time.RFC3339)), q)
	}
	if q.Limit < 0 {
		return Refuse(ReasonBadLimit, fmt.Sprintf("a limit of %d is not a limit", q.Limit), q)
	}
	return nil
}

// effectiveLimit clamps to the ceiling, and says so in a comment rather than
// silently.
//
// Silent clamping is what a UI spinner is for, and this is not a UI: a caller
// asking for 10,000 rows and receiving 200 has been given a different answer than
// the one they asked for, and the only way to find out is to compare.
func (q Question) effectiveLimit() int {
	if q.Limit <= 0 {
		return DefaultLimit
	}
	if q.Limit > MaxLimit {
		return MaxLimit
	}
	return q.Limit
}
