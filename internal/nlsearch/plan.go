package nlsearch

import (
	"strconv"
	"strings"
	"time"
)

// Plan: what the query is about to do, in words, before it does any of it.
//
// # Why a plan exists at all when the query is already safe
//
// Because "safe" and "what the operator asked for" are different questions, and a
// gateway that answers only the first will eventually answer the wrong question
// correctly. A plan is what turns "the query is bounded" into "here is the
// sentence you are about to get an answer to" — and the sentence is checkable by a
// person who cannot read SQL, which is most of the people who will use this.
//
// # What a plan must carry, and what it must not
//
// It carries: the tenant and sites, the window in absolute UTC, the kinds, the row
// ceiling, whether the answer is served by the index, and the SQL as a comment for
// the people who do read it.
//
// It does NOT carry the bound text as if it were a predicate. The operator's words
// are quoted as QUOTED — `websearch_to_tsquery('english', $4)` with $4 shown
// bound — because a plan that rendered the words inline would be a plan that looks
// like SQL and is not, and a reader who trusts the rendering would be trusting a
// thing this package did not produce.
//
// # The one thing it will not do
//
// Collapse the window. A window printed as "last night" is a window the operator
// cannot check, and "last night" near a midnight is a different answer from
// "last night" near a lunch. Every time in a plan is absolute UTC with an explicit
// offset, which is the same rule the rest of the product follows for the same
// reason.

// Plan is the readable rendering of a compiled query.
type Plan struct {
	// Tenant is the one tenant the query reads.
	Tenant string
	// Sites is what it covers, after intersection with the principal's scope.
	Sites []string
	// Since and Until are absolute UTC, zero meaning unbounded.
	Since time.Time
	Until time.Time
	// Kinds is which halves of the corpus the answer may come from.
	Kinds []SourceKind
	// Text is the operator's words, QUOTED and NOT a predicate.
	Text string
	// Limit is the row ceiling, already clamped.
	Limit int
	// MatchedIndexed reports whether every kind is served by the GIN index.
	MatchedIndexed bool
	// SQL is the statement, for a reader who wants it.
	SQL string
}

// PlanFor renders a compiled query.
func PlanFor(q *Query, asked Question) Plan {
	p := Plan{
		Tenant:         asked.TenantID,
		Sites:          append([]string(nil), q.Sites...),
		Since:          asked.Since,
		Until:          asked.Until,
		Kinds:          append([]SourceKind(nil), q.Kinds...),
		Text:           asked.Text,
		Limit:          q.Limit,
		MatchedIndexed: q.MatchedIndexed,
		SQL:            q.SQL,
	}
	return p
}

// String is the whole plan, one thing per line.
//
// Line-per-attribute rather than a paragraph because a plan is read by someone
// deciding whether to press a button, and a paragraph is something they skim.
func (p Plan) String() string {
	var b strings.Builder
	b.WriteString("search plan\n")
	b.WriteString("  tenant   " + p.Tenant + "\n")
	b.WriteString("  sites    " + strings.Join(p.Sites, ", ") + "\n")
	b.WriteString("  window   " + p.window() + "\n")
	b.WriteString("  sources  " + p.kindNames() + "\n")
	b.WriteString("  wording  " + strconv.Quote(p.Text) + "\n")
	b.WriteString("  ceiling  " + strconv.Itoa(p.Limit) + " rows\n")
	if p.MatchedIndexed {
		b.WriteString("  index    served by the GIN index on vector\n")
	} else {
		b.WriteString("  index    NOT served by an index for every source: the reviewer note and the\n")
		b.WriteString("           model output are matched with @@ and scanned. Correct, and slower on\n")
		b.WriteString("           a large corpus. Migration 0024's header says why.\n")
	}
	b.WriteString("  tables   " + strings.Join(AllowedTables, ", ") + " and nothing else\n")
	b.WriteString("  statement\n")
	for _, line := range strings.Split(strings.TrimRight(p.SQL, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

// window renders the absolute window, and says UNBOUNDED rather than guessing.
//
// "Unbounded" is a fact about the query and an operator can act on it; a default
// the plan invented would be a different query than the one described.
func (p Plan) window() string {
	if p.Since.IsZero() && p.Until.IsZero() {
		return "unbounded on both sides"
	}
	var b strings.Builder
	if p.Since.IsZero() {
		b.WriteString("from the beginning of the corpus")
	} else {
		b.WriteString("from " + p.Since.UTC().Format(time.RFC3339))
	}
	b.WriteString(" to ")
	if p.Until.IsZero() {
		b.WriteString("now")
	} else {
		b.WriteString(p.Until.UTC().Format(time.RFC3339))
	}
	return b.String()
}

// kindNames lists the source kinds, marking the ones an index serves.
//
// The marker is on the KIND rather than in a separate sentence, because the thing
// an operator needs is "the note my reviewer wrote will be searched by scanning",
// and that is a property of the row they care about.
func (p Plan) kindNames() string {
	parts := make([]string, 0, len(p.Kinds))
	for _, k := range p.Kinds {
		if k.Indexed() {
			parts = append(parts, string(k)+" (indexed)")
			continue
		}
		parts = append(parts, string(k)+" (matched, not indexed)")
	}
	return strings.Join(parts, ", ")
}
