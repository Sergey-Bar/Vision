package nlsearch

// The refusals, and why they are a type rather than an error string.
//
// # A refusal is not an error
//
// An error says "this did not work". A refusal says "this will not work, and here
// is the boundary". An operator who searches a site they cannot see and gets
// `permission denied` learns nothing they can act on; an operator who is told
// "that site is not in your scope, and here are the sites that are" learns the
// shape of their own access, which is a thing they can fix.
//
// So every refusal carries a [Reason] — a stable word a caller can branch on — a
// sentence written for a person, and the [Question] that caused it. The question
// goes back in the refusal because a refusal an operator cannot reproduce is a
// refusal they will hit again.
//
// # The reasons are exhaustive on purpose
//
// Fourteen reasons, and the set is closed by a test. A search gateway whose
// refusals are an open string space is a gateway whose callers will string-match,
// and string-matching a refusal is how a real refusal gets swallowed by a log
// filter.
//
// # What every reason has in common
//
// None of them is a TIMEOUT, a CAPACITY error, or anything about the database
// being unavailable. Those are errors. A refusal is a decision this package made
// before touching the database, and the distinction is the one a caller uses to
// decide whether to try again with different arguments or to stop.
import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Reason is a stable, branchable cause for a refusal.
//
// The values are lowercase with underscores because they appear in JSON and in log
// lines, and a space in either is a field somebody has to quote.
type Reason string

// The closed set.
const (
	// ReasonNoTenant is a question with no tenant. There is no "search everything"
	// in this product.
	ReasonNoTenant Reason = "no_tenant"
	// ReasonNoSites is a question that names no site, or an empty one.
	ReasonNoSites Reason = "no_sites"
	// ReasonUnknownSite is a question about a site outside the principal's scope.
	ReasonUnknownSite Reason = "site_out_of_scope"
	// ReasonUnknownKind is a `source_kind` outside the closed set.
	ReasonUnknownKind Reason = "unknown_source_kind"
	// ReasonInvertedWindow is a window that ends before it starts.
	ReasonInvertedWindow Reason = "inverted_window"
	// ReasonBadLimit is a negative limit.
	ReasonBadLimit Reason = "bad_limit"
	// ReasonUnpinnedHost is a request to a host that is not the committed one.
	ReasonUnpinnedHost Reason = "unpinned_host"
	// ReasonHostMismatch is a request whose host disagrees with the manifest.
	ReasonHostMismatch Reason = "host_manifest_mismatch"
	// ReasonPromptMismatch is a request whose prompt does not hash to the committed
	// digest. Somebody edited the prompt without re-pinning it, and the search is
	// about to ask a model something nobody reviewed.
	ReasonPromptMismatch Reason = "prompt_digest_mismatch"
	// ReasonTableNotAllowed is a question needing a relation this gateway does not
	// search.
	ReasonTableNotAllowed Reason = "table_not_allowed"
	// ReasonTenantPredicateDropped is the compile-time invariant that no statement
	// was produced without `tenant_id = $1`. It cannot happen by construction and it
	// has a reason anyway, because an invariant with no name is an invariant nobody
	// is told was violated.
	ReasonTenantPredicateDropped Reason = "tenant_predicate_dropped"
	// ReasonSiteScopeWidened is the compile-time invariant that no statement was
	// produced covering a site outside the question's.
	ReasonSiteScopeWidened Reason = "site_scope_widened"
	// ReasonUnparseable is a question whose words match no known form.
	ReasonUnparseable Reason = "unparseable_question"
)

// allReasons is the closed set, and the test that it is closed asserts against this
// list rather than against the code, so a reason added without a case fails.
var allReasons = []Reason{
	ReasonNoTenant, ReasonNoSites, ReasonUnknownSite, ReasonUnknownKind,
	ReasonInvertedWindow, ReasonBadLimit, ReasonUnpinnedHost, ReasonHostMismatch,
	ReasonPromptMismatch, ReasonTableNotAllowed, ReasonTenantPredicateDropped,
	ReasonSiteScopeWidened, ReasonUnparseable,
}

// Reasons returns every reason, sorted, for a caller's help text and for the test
// that keeps the set closed.
func Reasons() []Reason {
	out := append([]Reason(nil), allReasons...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Refusal is this package's decision, with everything a caller needs to print it
// and nothing it needs to guess at.
type Refusal struct {
	// Reason is the branchable word.
	Reason Reason
	// Message is a whole sentence written for a person reading a console. It never
	// contains SQL, a column name, or anything the operator cannot act on.
	Message string
	// Question is what was refused, so the refusal is reproducible.
	Question Question
	// SitesInScope is the principal's own scope, when the refusal is about scope.
	//
	// Present because "you cannot see north-dock" is half an answer and the other
	// half is "you can see north-gate". An operator who is told only the first will
	// ask a colleague instead of finding out.
	SitesInScope []string
	// At is when the refusal was decided, so a report of refusals can be correlated
	// with a report of results.
	At time.Time
}

// Refuse builds a refusal.
//
// `now` is a parameter rather than a call to time.Now because a refusal that
// cannot be reproduced in a test is a refusal whose text nobody can check.
func Refuse(reason Reason, message string, q Question) *Refusal {
	return &Refusal{Reason: reason, Message: message, Question: q, At: time.Now().UTC()}
}

// RefuseWithScope is [Refuse] plus the principal's scope, for the scope refusals.
func RefuseWithScope(reason Reason, message string, q Question, inScope []string) *Refusal {
	r := Refuse(reason, message, q)
	r.SitesInScope = append([]string(nil), inScope...)
	return r
}

// Error makes a refusal an error, so it satisfies `error` and a caller that only
// checks `err != nil` still stops.
//
// The message is the reason's name first, because that is what a log filter
// matches on, and the sentence after it because that is what a person reads. The
// order is deliberate and the reverse of the usual.
func (r *Refusal) Error() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("nlsearch: refused (%s): %s", r.Reason, r.Message)
}

// String renders it for a console, with the scope named when the refusal is about
// scope.
func (r *Refusal) String() string {
	if r == nil {
		return "no refusal"
	}
	var b strings.Builder
	b.WriteString(string(r.Reason))
	b.WriteString(": ")
	b.WriteString(r.Message)
	if len(r.SitesInScope) > 0 {
		b.WriteString(" Sites you can search: ")
		b.WriteString(strings.Join(r.SitesInScope, ", "))
		b.WriteString(".")
	}
	return b.String()
}
