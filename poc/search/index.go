// Package search provides a searchable index for video clips with categories and spatial alerts.
package search

import (
	"log"
	"strings"
	"sync"
	"time"
)

// Index is a thread-safe searchable index of video clip entries.
type Index struct {
	entries    []Entry
	mu         sync.RWMutex
	retention  time.Duration
	maxResults int
	stopCh     chan struct{}
}

// Entry represents a searchable video clip entry.
type Entry struct {
	ID            string         `json:"id"`
	EvidenceID    string         `json:"evidence_id"`
	EvidenceHash  string         `json:"evidence_hash"`
	Timestamp     time.Time      `json:"timestamp"`
	Categories    []CategoryInfo `json:"categories"`
	SpatialAlerts []SpatialAlert `json:"spatial_alerts"`
	ClipDuration  time.Duration  `json:"clip_duration"`
	Resolution    string         `json:"resolution"`
}

// CategoryInfo represents a detected object category in a clip.
type CategoryInfo struct {
	Category    string  `json:"category"`
	SubCategory string  `json:"sub_category"`
	Label       string  `json:"label"`
	Confidence  float64 `json:"confidence"`
}

// SpatialAlert represents a spatial alert associated with a clip.
type SpatialAlert struct {
	ZoneID         string `json:"zone_id"`
	ZoneName       string `json:"zone_name"`
	AlertType      string `json:"alert_type"`
	ObjectLabel    string `json:"object_label"`
	ObjectCategory string `json:"object_category"`
	Severity       string `json:"severity"`
	Timestamp      int64  `json:"timestamp"`
}

// Query defines search parameters.
type Query struct {
	Text          string    `json:"text"`
	Categories    []string  `json:"categories,omitempty"`
	Zones         []string  `json:"zones,omitempty"`
	TimeRange     TimeRange `json:"time_range,omitempty"`
	MinConfidence float64   `json:"min_confidence,omitempty"`
	Limit         int       `json:"limit,omitempty"`
}

// TimeRange defines a time window for search.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Result contains search results.
type Result struct {
	Entries   []Entry `json:"entries"`
	Total     int     `json:"total"`
	QueryTime int64   `json:"query_time_ms"`
}

// NewIndex creates a new search index with retention and max results limits.
func NewIndex(retention time.Duration, maxResults int) *Index {
	idx := &Index{
		entries:    make([]Entry, 0),
		retention:  retention,
		maxResults: maxResults,
		stopCh:     make(chan struct{}),
	}
	go idx.cleanupLoop()
	return idx
}

// Add adds an entry to the index.
func (idx *Index) Add(entry Entry) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.entries = append(idx.entries, entry)
}

// Search queries the index and returns matching entries.
func (idx *Index) Search(query Query) Result {
	start := time.Now()

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var results []Entry
	for _, entry := range idx.entries {
		if idx.matchesQuery(entry, query) {
			results = append(results, entry)
		}
	}

	limit := query.Limit
	if limit <= 0 || limit > idx.maxResults {
		limit = idx.maxResults
	}
	if len(results) > limit {
		results = results[:limit]
	}

	return Result{
		Entries:   results,
		Total:     len(results),
		QueryTime: time.Since(start).Milliseconds(),
	}
}

func (idx *Index) matchesQuery(entry Entry, query Query) bool {
	if query.Text != "" {
		if !idx.textMatch(entry, query.Text) {
			return false
		}
	}

	if len(query.Categories) > 0 {
		if !idx.categoryMatch(entry, query.Categories) {
			return false
		}
	}

	if len(query.Zones) > 0 {
		if !idx.zoneMatch(entry, query.Zones) {
			return false
		}
	}

	if !query.TimeRange.Start.IsZero() || !query.TimeRange.End.IsZero() {
		if !idx.timeMatch(entry, query.TimeRange) {
			return false
		}
	}

	if query.MinConfidence > 0 {
		if !idx.confidenceMatch(entry, query.MinConfidence) {
			return false
		}
	}

	return true
}

func (idx *Index) textMatch(entry Entry, text string) bool {
	// Every token must match somewhere ("red car" matches an entry that has
	// a red sedan: "red" hits the label, "car" hits the sub-category).
	for _, token := range strings.Fields(strings.ToLower(text)) {
		if !tokenMatch(entry, token) {
			return false
		}
	}
	return true
}

func tokenMatch(entry Entry, token string) bool {
	for _, cat := range entry.Categories {
		if containsLower(cat.Label, token) ||
			containsLower(cat.Category, token) ||
			containsLower(cat.SubCategory, token) {
			return true
		}
	}
	for _, alert := range entry.SpatialAlerts {
		if containsLower(alert.ZoneName, token) ||
			containsLower(alert.ObjectLabel, token) ||
			containsLower(alert.ObjectCategory, token) {
			return true
		}
	}
	return false
}

func (idx *Index) categoryMatch(entry Entry, categories []string) bool {
	for _, cat := range entry.Categories {
		for _, qcat := range categories {
			if cat.Category == qcat || cat.SubCategory == qcat {
				return true
			}
		}
	}
	return false
}

func (idx *Index) zoneMatch(entry Entry, zones []string) bool {
	for _, alert := range entry.SpatialAlerts {
		for _, z := range zones {
			if alert.ZoneID == z || alert.ZoneName == z {
				return true
			}
		}
	}
	return false
}

func (idx *Index) timeMatch(entry Entry, tr TimeRange) bool {
	if !tr.Start.IsZero() && entry.Timestamp.Before(tr.Start) {
		return false
	}
	if !tr.End.IsZero() && entry.Timestamp.After(tr.End) {
		return false
	}
	return true
}

func (idx *Index) confidenceMatch(entry Entry, minConf float64) bool {
	for _, cat := range entry.Categories {
		if cat.Confidence >= minConf {
			return true
		}
	}
	return false
}

// Count returns the number of entries in the index.
func (idx *Index) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

func (idx *Index) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-idx.stopCh:
			return
		case <-ticker.C:
			idx.cleanup()
		}
	}
}

func (idx *Index) cleanup() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	cutoff := time.Now().Add(-idx.retention)
	var kept []Entry
	for _, entry := range idx.entries {
		if entry.Timestamp.After(cutoff) {
			kept = append(kept, entry)
		}
	}
	removed := len(idx.entries) - len(kept)
	if removed > 0 {
		log.Printf("Search index: removed %d expired entries", removed)
		idx.entries = kept
	}
}

// Stop stops the cleanup loop.
func (idx *Index) Stop() {
	close(idx.stopCh)
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i, b := range []byte(s) {
		if b >= 'A' && b <= 'Z' {
			result[i] = b + 32
		} else {
			result[i] = b
		}
	}
	return string(result)
}

func containsLower(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexLower(s, substr) >= 0)
}

func indexLower(s, substr string) int {
	sLower := toLower(s)
	subLower := toLower(substr)
	for i := 0; i <= len(sLower)-len(subLower); i++ {
		if sLower[i:i+len(subLower)] == subLower {
			return i
		}
	}
	return -1
}
