// Package eventsearch answers the question an operator actually asks.
//
// The question is never "give me the canonical events" — it is "what happened at
// Door 18 between 14:00 and 15:00", and the plan's C5 is exactly that: SQL over
// the `canonical_events` windows, behind a capability, with a console page beside
// it. What this package adds to the table is the shape of the answer: every hit
// carries the incident it belongs to when there is one, and nothing at all when
// there is not. A hit that invented an incident id to look complete would be a
// fabricated link, which is the failure the whole event record is built to
// refuse — `master-roadmap.md` on the M1 model: *"official state is the incident
// row, not an observation"*, and this is the same rule applied to search.
//
// # The time window reads one column
//
// `normalized_at`, and nothing else. `observed_at` is what the source claimed,
// `received_at` is our ingest, and `normalized_at` is drift-corrected — the only
// field a time-window predicate may read, because a window over a source clock
// is a window over a clock the site's NTP owns. `internal/predicatecov` holds
// the Go source to that rule; this package holds its SQL to it too, and the test
// that proves it inserts an event whose source time is inside the window and
// whose normalized time is not, and expects nothing back.
//
// # The site scope is a parameter, never a default
//
// A search without a site scope is a search across every site the caller may
// see, so the caller supplies the scope and an empty scope returns nothing.
// That is the same shape the incident list carries, and for the same reason: an
// empty result and a refused one must not be the same answer, and "no sites"
// must not read as "every site".
package eventsearch

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/jackc/pgx/v5"
)

// Embedder turns a free-text query into a vector the record can be searched by.
//
// Nil means no embedding model is configured, and free-text search degrades to
// the keyword path rather than failing. A real implementation (a local
// sentence-transformers model, or a Python sidecar) satisfies this interface
// and is wired in at service construction; the search surface itself never
// depends on a particular model.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Query is one structured search.
//
// Zero-valued time fields mean "no bound on that side", and the site list is
// the caller's scope rather than a filter: the handler resolves it from the
// principal, so a query that crossed a site boundary would have to be
// constructed by a caller that had already decided to.
//
// `FreeText` turns the search into a semantic/keyword search over the event
// record rather than a structured field match. When set, the structured fields
// still narrow the result (site, window) and the free text ranks it.
type Query struct {
	SiteIDs       []string
	EventType     string
	DeviceID      string
	ZoneID        string
	CorrelationID string
	IncidentID    string
	From          time.Time
	To            time.Time
	Limit         int
	Cursor        string
	// FreeText is one free-text query. Empty means structured search only.
	FreeText string
}

// Hit is one canonical event that matched.
//
// The times are both returned because they answer different questions:
// `observedAt` is what the camera said, `normalizedAt` is what the window read,
// and an operator reconciling a hit against a wall clock needs to see the gap
// rather than infer it.
type Hit struct {
	EventID       string    `json:"eventId"`
	IncidentID    string    `json:"incidentId,omitempty"`
	SiteID        string    `json:"siteId"`
	EventType     string    `json:"eventType"`
	DeviceID      string    `json:"deviceId,omitempty"`
	ZoneID        string    `json:"zoneId,omitempty"`
	CorrelationID string    `json:"correlationId,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
	NormalizedAt  time.Time `json:"normalizedAt"`
}

// Result is a page and the cursor that follows it.
type Result struct {
	Hits       []Hit  `json:"hits"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// Service searches the canonical record.
type Service struct {
	Pool *dbx.Pool
	// Embed is the embedding model, when one is configured. Nil degrades
	// free-text search to the keyword path rather than failing.
	Embed Embedder

	hasVector     *bool // lazily probed: is pgvector installed in the target database?
	hasVectorOnce sync.Once
}

// DefaultLimit and MaxLimit bound the page. A search that returned everything
// would be a search that returned nothing usable, and the window predicate is
// the expensive part of the query — a caller asking for everything is asking
// for the BRIN index to be useless.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// searchSQL joins the incident link rather than inventing one.
//
// The LEFT JOIN through `incident_events` is what makes the hit answerable: an
// event with no incident row has a NULL incident id, which marshals away, and
// an event with one carries it. The window reads `normalized_at` only — see the
// package comment. The ORDER BY is (normalized_at, event_id) so the keyset
// cursor is total: two events sharing a normalized time still order stably, and
// a page boundary cannot skip or repeat one.
const searchSQL = `
SELECT e.event_id, e.site_id, e.event_type, e.device_id, e.zone_id,
       e.correlation_id, e.observed_at, e.normalized_at,
       ie.incident_id
  FROM canonical_events e
  LEFT JOIN incident_events ie
         ON ie.tenant_id = e.tenant_id AND ie.event_id = e.event_id
 WHERE e.tenant_id = $1
   AND ($2::text[] IS NULL OR e.site_id = ANY($2::text[]))
   AND ($3 = '' OR e.event_type = $3)
   AND ($4 = '' OR e.device_id = $4)
   AND ($5 = '' OR e.zone_id = $5)
   AND ($6 = '' OR e.correlation_id = $6)
   AND ($7 = '' OR ie.incident_id = $7)
   AND ($8::timestamptz IS NULL OR e.normalized_at >= $8)
   AND ($9::timestamptz IS NULL OR e.normalized_at <= $9)
    AND ($10::timestamptz IS NULL OR (e.normalized_at, e.event_id) < ($10::timestamptz, $11))
 ORDER BY e.normalized_at DESC, e.event_id DESC
 LIMIT $12`

// freeTextVectorSQL ranks events by cosine distance to the query embedding.
//
// `em.embedding <=> $5::vector` is cosine distance: smaller is more similar, so
// ascending order surfaces the most similar events first. The structured fields
// (site, window) still narrow the candidate set before ranking.
const freeTextVectorSQL = `
SELECT e.event_id, e.site_id, e.event_type, e.device_id, e.zone_id,
       e.correlation_id, e.observed_at, e.normalized_at,
       ie.incident_id
  FROM event_embeddings em
  JOIN canonical_events e
    ON e.tenant_id = em.tenant_id AND e.event_id = em.event_id
  LEFT JOIN incident_events ie
    ON ie.tenant_id = e.tenant_id AND ie.event_id = e.event_id
 WHERE em.tenant_id = $1
   AND ($2::text[] IS NULL OR e.site_id = ANY($2::text[]))
   AND ($3::timestamptz IS NULL OR e.normalized_at >= $3)
   AND ($4::timestamptz IS NULL OR e.normalized_at <= $4)
 ORDER BY em.embedding <=> $5::vector
 LIMIT $6`

// freeTextKeywordSQL is the fallback when no embedding model or pgvector is
// available. It matches the query's words against the stored document text and
// ranks by recency, which is the honest "we cannot rank by meaning" answer.
const freeTextKeywordSQL = `
SELECT e.event_id, e.site_id, e.event_type, e.device_id, e.zone_id,
       e.correlation_id, e.observed_at, e.normalized_at,
       ie.incident_id
  FROM event_embeddings em
  JOIN canonical_events e
    ON e.tenant_id = em.tenant_id AND e.event_id = em.event_id
  LEFT JOIN incident_events ie
    ON ie.tenant_id = e.tenant_id AND ie.event_id = e.event_id
 WHERE em.tenant_id = $1
   AND ($2::text[] IS NULL OR e.site_id = ANY($2::text[]))
   AND ($3::timestamptz IS NULL OR e.normalized_at >= $3)
   AND ($4::timestamptz IS NULL OR e.normalized_at <= $4)
   AND (to_tsvector('english', em.document) @@ plainto_tsquery('english', $5)
        OR em.document ILIKE '%' || $5 || '%')
 ORDER BY e.normalized_at DESC, e.event_id DESC
 LIMIT $6`

// Search answers one query as one page.
//
// The tenant and the site scope are parameters rather than anything read from
// the request, because a search is a read of somebody's events and both halves
// of "somebody" belong to the caller who resolved them. An empty site list
// returns an empty page: the caller has nothing in scope, and a search that
// widened itself to "everything" would be the disclosure this package exists to
// avoid.
func (s *Service) Search(ctx context.Context, tenant string, q Query) (Result, error) {
	if s == nil || s.Pool == nil {
		return Result{}, errors.New("eventsearch: no pool")
	}
	limit := q.Limit
	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}
	sites := cleanSites(q.SiteIDs)
	var from, to, cursorAt any
	if !q.From.IsZero() {
		from = q.From.UTC()
	}
	if !q.To.IsZero() {
		to = q.To.UTC()
	}
	cursorID := ""
	if q.Cursor != "" {
		at, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return Result{}, fmt.Errorf("eventsearch: cursor: %w", err)
		}
		cursorAt, cursorID = at, id
	}

	// If we have free text and no other structured parameters specified,
	// try to parse it as natural language to govern the search
	if q.FreeText != "" && len(q.SiteIDs) == 0 && q.EventType == "" && q.DeviceID == "" &&
		q.ZoneID == "" && q.CorrelationID == "" && q.IncidentID == "" &&
		q.From.IsZero() && q.To.IsZero() {
		// Parse natural language to add governance
		nlQuery := parseNaturalLanguage(q.FreeText)
		// Only override if we found something specific
		if nlQuery.EventType != "" {
			q.EventType = nlQuery.EventType
		}
		if !nlQuery.From.IsZero() {
			q.From = nlQuery.From
		}
		if !nlQuery.To.IsZero() {
			q.To = nlQuery.To
		}
		// Keep the original free text for ranking
	}

	// `WithSites` rather than `For`, so the site-scoped policy the database
	// enforces sees the same scope the handler checked. `For` alone would bind
	// the tenant and nothing else, and a search that asked for site-a while
	// holding site-b's context would be answered by the policy instead of by
	// this service — a right answer for the wrong reason.
	if q.FreeText != "" {
		return s.freeTextSearch(ctx, tenant, q, limit, sites, from, to)
	}
	rows, err := s.Pool.WithSites(tenant, "eventsearch", sites).Query(ctx, searchSQL,
		tenant, sites, q.EventType, q.DeviceID, q.ZoneID, q.CorrelationID,
		q.IncidentID, from, to, cursorAt, cursorID, limit+1)
	if err != nil {
		return Result{}, fmt.Errorf("eventsearch: search: %w", err)
	}
	defer rows.Close()

	hits := make([]Hit, 0, limit)
	for rows.Next() {
		var h Hit
		var incidentID *string
		if err := rows.Scan(&h.EventID, &h.SiteID, &h.EventType, &h.DeviceID,
			&h.ZoneID, &h.CorrelationID, &h.ObservedAt, &h.NormalizedAt, &incidentID); err != nil {
			return Result{}, fmt.Errorf("eventsearch: scan: %w", err)
		}
		if incidentID != nil {
			h.IncidentID = *incidentID
		}
		hits = append(hits, h)
		if len(hits) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("eventsearch: iterate: %w", err)
	}
	out := Result{Hits: hits}
	if len(hits) > limit {
		last := hits[limit-1]
		out.Hits = hits[:limit]
		out.NextCursor = encodeCursor(last.NormalizedAt, last.EventID)
	}
	return out, nil
}

// freeTextSearch answers a free-text query over the event record.
//
// Every hit still resolves through `canonical_events`, so a free-text hit is as
// citable as a structured one. When an embedding model and pgvector are both
// available the results rank by cosine similarity; otherwise they fall back to
// keyword matching over the stored document text. Neither path fabricates
// incidents: an event with no incident link carries an empty incident id.
func (s *Service) freeTextSearch(ctx context.Context, tenant string, q Query, limit int, sites []string, from, to any) (Result, error) {
	vector, useVector, err := s.queryVector(ctx, q.FreeText)
	if err != nil {
		return Result{}, fmt.Errorf("eventsearch: free text: %w", err)
	}
	if useVector {
		rows, err := s.Pool.WithSites(tenant, "eventsearch", sites).Query(ctx, freeTextVectorSQL,
			tenant, sites, from, to, formatVector(vector), limit)
		if err != nil {
			return Result{}, fmt.Errorf("eventsearch: free text vector: %w", err)
		}
		defer rows.Close()
		return s.scanFreeText(rows, limit)
	}
	rows, err := s.Pool.WithSites(tenant, "eventsearch", sites).Query(ctx, freeTextKeywordSQL,
		tenant, sites, from, to, q.FreeText, limit)
	if err != nil {
		return Result{}, fmt.Errorf("eventsearch: free text keyword: %w", err)
	}
	defer rows.Close()
	return s.scanFreeText(rows, limit)
}

// queryVector embeds the free text and reports whether pgvector is available.
//
// pgvector is probed once and cached: when it is absent the keyword path is
// used even if an embedding model is configured, and no query ever fails
// because the extension was not installed.
func (s *Service) queryVector(ctx context.Context, text string) ([]float32, bool, error) {
	available, err := s.vectorAvailable(ctx)
	if err != nil {
		return nil, false, err
	}
	if !available || s.Embed == nil {
		return nil, false, nil
	}
	vector, err := s.Embed.Embed(ctx, text)
	if err != nil {
		return nil, false, fmt.Errorf("embed: %w", err)
	}
	return vector, true, nil
}

func (s *Service) vectorAvailable(ctx context.Context) (bool, error) {
	s.hasVectorOnce.Do(func() {
		var n int
		err := s.Pool.QueryRow(ctx, `SELECT 1 FROM pg_extension WHERE extname='vector'`).Scan(&n)
		if err != nil {
			s.hasVector = ptr(false)
			return
		}
		s.hasVector = ptr(true)
	})
	if s.hasVector == nil {
		return false, errors.New("eventsearch: pgvector probe did not run")
	}
	return *s.hasVector, nil
}

func ptr(b bool) *bool { return &b }

func (s *Service) scanFreeText(rows pgx.Rows, limit int) (Result, error) {
	defer rows.Close()
	hits := make([]Hit, 0, limit)
	for rows.Next() {
		var h Hit
		var incidentID *string
		if err := rows.Scan(&h.EventID, &h.SiteID, &h.EventType, &h.DeviceID,
			&h.ZoneID, &h.CorrelationID, &h.ObservedAt, &h.NormalizedAt, &incidentID); err != nil {
			return Result{}, fmt.Errorf("eventsearch: scan: %w", err)
		}
		if incidentID != nil {
			h.IncidentID = *incidentID
		}
		hits = append(hits, h)
		if len(hits) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("eventsearch: iterate: %w", err)
	}
	return Result{Hits: hits}, nil
}

// formatVector renders a float32 vector as a pgvector literal.
//
// The caller generates the floats, so there is no injection surface: the value
// is cast to `vector` by PostgreSQL and never concatenated raw.
func formatVector(v []float32) string {
	b := strings.Builder{}
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// cleanSites normalises the caller's scope for the SQL.
//
// The result is never nil: pgx sends a nil slice as SQL NULL, and the query
// treats a NULL array as "no site filter" — which would make an unscoped
// principal a principal scoped to everything. An empty array is what an empty
// scope looks like on the wire, and `= ANY('{}')` matches nothing, which is the
// honest answer. This is the difference between "no sites" and "every site",
// stated in the one place both of them are decided.
func cleanSites(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// parseNaturalLanguage converts a free-text query into a structured query by
// mapping simple keywords to governed parameters. This is a simplified
// implementation for demo purposes.
// Examples:
//
//	"person running" → EventType=motion (assuming motion detection identifies persons)
//	"at door 18" → would need camera/zone mapping, but we'll leave as free text for now
//	"last hour" → sets time window
func parseNaturalLanguage(text string) Query {
	// Convert to lowercase for case-insensitive matching
	text = strings.ToLower(text)
	query := Query{}

	// Simple keyword mapping for demo
	// In a real implementation, this would use NLP techniques
	if strings.Contains(text, "person") || strings.Contains(text, "people") {
		// Assume person detection falls under motion events for simplicity
		query.EventType = "motion"
	}
	if strings.Contains(text, "running") || strings.Contains(text, "walking") {
		// Movement activity
		query.EventType = "motion"
	}
	// A door, an entrance or an exit would map to specific zones and cameras in
	// a real implementation. For the demo they are left in the free text, where
	// the keyword path can still find the events that mention them.
	if strings.Contains(text, "car") || strings.Contains(text, "vehicle") {
		// Vehicle detection
		query.EventType = "motion" // Simplified
	}

	// Time-based phrases
	switch {
	case strings.Contains(text, "last hour") || strings.Contains(text, "past hour"):
		query.From = time.Now().Add(-time.Hour)
		query.To = time.Now()
	case strings.Contains(text, "last 24 hours") || strings.Contains(text, "past day"):
		query.From = time.Now().Add(-24 * time.Hour)
		query.To = time.Now()
	case strings.Contains(text, "today"):
		todayStart := time.Now().Truncate(24 * time.Hour)
		query.From = todayStart
		query.To = time.Now()
	}

	// Any remaining text goes to FreeText for keyword/vector search
	query.FreeText = text

	return query
}

// encodeCursor makes the keyset position opaque.
//
// The cursor is not an index and not a row id: it is the last row's sort key,
// which is what makes the pagination stable across inserts. It is base64 rather
// than plain text so a caller cannot read it as "the query so far" and hand it
// back after the data changed meaning.
func encodeCursor(at time.Time, id string) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cur string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return time.Time{}, "", errors.New("not a cursor this server issued")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("malformed cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", errors.New("cursor carries an unreadable time")
	}
	return at, parts[1], nil
}
