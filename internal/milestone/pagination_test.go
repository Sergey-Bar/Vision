package milestone_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/milestone"
)

// pagedTransport serves a feed in pages, the way a real paginated API does, and
// records the cursor each request asked for.
type pagedTransport struct {
	mu       sync.Mutex
	pages    []string // one JSON array per page
	cursors  []string // the Next-Cursor each page hands out, "" for the last
	askedFor []string
	// cursorFor counts pages handed out per route.
	//
	// It was one counter for every route, which meant the order of the adapter's
	// requests decided which page each collection got: adding one read ahead of
	// the feeds shifted every feed onto page two, and a test asserting that three
	// pages were followed failed for a reason that had nothing to do with
	// pagination. A real server pages each collection independently, so this does
	// too - and the assertion it protects gets stronger, because now every feed
	// has to reach the third page, not whichever one happened to start at page 0.
	cursorFor map[string]int
}

func (p *pagedTransport) Get(_ context.Context, route milestone.Route, q milestone.Query) (*milestone.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cursorFor == nil {
		p.cursorFor = map[string]int{}
	}
	i := p.cursorFor[route.Path]
	p.cursorFor[route.Path] = i + 1
	p.askedFor = append(p.askedFor, q.Cursor)
	if i >= len(p.pages) {
		i = len(p.pages) - 1
	}
	h := map[string]string{}
	if i < len(p.cursors) && p.cursors[i] != "" {
		h["Next-Cursor"] = p.cursors[i]
	}
	return &milestone.Response{Status: 200, Headers: h, Body: json.RawMessage(p.pages[i])}, nil
}

func (p *pagedTransport) Close() error { return nil }

func page(from, n int) string {
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{
			"Id":        fmt.Sprintf("evt-%03d", from+i),
			"Type":      "Motion",
			"Timestamp": "2026-03-01T09:00:00Z",
			"Source":    "Entrance Camera 01 [cam-1]",
		})
	}
	body, _ := json.Marshal(items)
	return string(body)
}

func adapterOver(tr milestone.Transport) *milestone.Adapter {
	return milestone.New(tr, "site-1", "int-1").
		WithClock(func() time.Time { return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) })
}

// A feed with more events than fit in one page used to be read one page deep.
//
// `transport.Get` captures the `Next-Cursor` header into `Response.Headers` —
// deliberately, with a comment saying so - and nothing followed it. The poll
// then advanced `last_success_at` to the newest received_at of the records it
// did read, so the next poll asked for `since = now` and every event after the
// first 200 was never requested again.
//
// That is invariant 1: accepted events, gone permanently, with no dead letter,
// because the loop never learned they existed.
func TestAFeedIsReadToTheEndAcrossEveryPage(t *testing.T) {
	tr := &pagedTransport{
		pages:   []string{page(0, 3), page(3, 3), page(6, 2)},
		cursors: []string{"c2", "c3", ""},
	}
	records, err := adapterOver(tr).Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	// Every feed is paginated and every one of them gets the same stub pages, so
	// the count is per-feed. The property being asserted is that one feed's
	// three pages produced eight distinct events, so the events are counted by
	// id rather than by record.
	ids := map[string]bool{}
	for _, r := range records {
		if strings.HasPrefix(r.SourceEventID, "evt-") {
			ids[r.SourceEventID] = true
		}
	}
	if len(ids) != 8 {
		t.Errorf("the feed yielded %d distinct events, want 8 across three pages. Every event "+
			"after the first page was never requested again (and %d records came back in "+
			"total, because several feeds are paginated and all of them use this stub)",
			len(ids), len(records))
	}
	// Each page must have been asked for by the cursor the previous one gave.
	want := []string{"", "c2", "c3"}
	seen := map[string]int{}
	for _, got := range tr.askedFor {
		seen[got]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			t.Errorf("no request was made with cursor %q; the pagination chain was not followed",
				w)
		}
	}
}

// A cursor that does not advance is a server bug, and following it for ever is
// worse than stopping. Stopping silently would be the same defect as the one
// above in a different shape, so the truncation is reported.
//
// The report arrives as a malformed record rather than as an error from Poll:
// Poll deliberately converts a per-feed failure into a record so one endpoint
// being down does not abort the whole poll, and the cursor problem is a
// per-feed failure like any other.
func TestARepeatedCursorStopsAndSaysSo(t *testing.T) {
	tr := &pagedTransport{
		pages:   []string{page(0, 2), page(2, 2)},
		cursors: []string{"same", "same"},
	}
	records, err := adapterOver(tr).Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	// The loop must have stopped: two pages, not an unbounded number of requests.
	if len(tr.askedFor) > 2*len(milestone.Routes) {
		t.Errorf("the adapter made %d requests for %d feeds. A cursor that does not advance "+
			"was followed rather than refused", len(tr.askedFor), len(milestone.Routes))
	}
	var complained bool
	for _, r := range records {
		if strings.Contains(r.MalformedReason, "cursor") {
			complained = true
		}
	}
	if !complained {
		t.Error("a server handing back the same cursor twice was followed without saying so. " +
			"Silent truncation is the same defect as never following the cursor at all")
	}
}

// A feed that cannot be read produced a record with no source id, and
// `pipeline.Poll` drops exactly those — so the outage left no inbox row, no
// dead letter and no counter, and the ingest-loss SLO saw a clean feed.
//
// An outage the thing that watches for outages cannot see is not an outage that
// gets noticed.
func TestAnUnreadableFeedProducesARecordThatCanBeStored(t *testing.T) {
	a := adapterOver(&failingTransport{err: fmt.Errorf("milestone returned status 500")})
	records, err := a.Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll returned an error instead of a record: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("an unreadable feed produced no record at all")
	}
	// Every feed is unreadable here, so every record must be storable.
	for _, r := range records {
		if r.SourceEventID == "" {
			t.Errorf("the outage record for %q has no source id. pipeline.Poll drops records "+
				"with no source id, so this is exactly the record that was thrown away",
				r.EventType)
		}
		if r.SiteID == "" {
			t.Error("the outage record has no site id, so it cannot be scoped to a site")
		}
		if !r.Malformed {
			t.Error("the outage record is not marked malformed")
		}
		if !strings.Contains(r.SourceEventID, "feed-error") {
			t.Errorf("the outage id is %q; it should name itself as a feed error so an operator "+
				"reading the dead-letter table can tell it from an unreadable event",
				r.SourceEventID)
		}
	}
}

// And it is the same id on the next poll, so a feed that has been down for an
// hour is one outstanding fact and not sixty dead letters.
func TestAnUnreadableFeedsIdentityIsStableAcrossPolls(t *testing.T) {
	a := adapterOver(&failingTransport{err: fmt.Errorf("milestone returned status 500")})
	since := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	first, err := a.Poll(context.Background(), &since)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	second, err := a.Poll(context.Background(), &since)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	byID := map[string]bool{}
	for _, r := range first {
		byID[r.SourceEventID] = true
	}
	for _, r := range second {
		if !byID[r.SourceEventID] {
			t.Errorf("the same failing feed in the same window produced a new id %q. An id that "+
				"changes per attempt turns an hour-long outage into sixty dead letters",
				r.SourceEventID)
		}
	}
}

// Two unreadable items at the same position on different pages were given the
// same id, and the inbox deduplicated the second away: one silently lost event
// per page. The id is derived from the item's content instead.
func TestTwoDistinctUnreadableItemsDoNotCollide(t *testing.T) {
	// The two unreadable items are genuinely different events - different
	// timestamps, different types - that happen to sit at the same position in
	// their pages. The old id was "<integration>:<path>:unparsed:<index within
	// this page>", so they collided and the second was deduplicated away.
	tr := &pagedTransport{
		pages: []string{
			`[{"Id":"a","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"},
			  {"Id":"","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"}]`,
			`[{"Id":"b","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"},
			  {"Id":"","Type":"Analytics","Timestamp":"2026-03-01T10:00:00Z"}]`,
		},
		cursors: []string{"p2", ""},
	}
	records, err := adapterOver(tr).Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	unparsed := map[string]int{}
	for _, r := range records {
		if strings.Contains(r.SourceEventID, ":unparsed:") {
			unparsed[r.SourceEventID]++
		}
	}
	if len(unparsed) < 2 {
		t.Errorf("got %d distinct unparsed-item identities, want at least 2: two genuinely "+
			"different unreadable events at the same position in their pages were given the "+
			"same id, and the second would be deduplicated away. Ids: %v", len(unparsed), unparsed)
	}
	for id, n := range unparsed {
		if n > 1 {
			t.Errorf("unparsed id %q appears %d times; two different unreadable items collided",
				id, n)
		}
	}
}

type failingTransport struct{ err error }

func (f *failingTransport) Get(context.Context, milestone.Route, milestone.Query) (*milestone.Response, error) {
	return nil, f.err
}
func (f *failingTransport) Close() error { return nil }
