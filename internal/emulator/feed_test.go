package emulator

// The vendor feed's pagination.
//
// # The defect this is about
//
// The vendor's documented continuation is `?page=<n>` plus a `_links.next`
// value in the body, and the adapter FOLLOWS it. Without that link the adapter
// reads the first page and stops -- and the events on the second page, and on
// every page after it, are never ingested at all.
//
// That is invariant 1 broken on the vendor's side of the wire: accepted events
// lost silently, with no dead letter, because the reader never learned they
// existed.
//
// # What the test does
//
// It walks the feed the way the adapter walks it. It asks for a small page,
// takes the `_links.next` the body carries, follows it, and asserts that the
// events on the NEXT page arrive.
//
// It asserts determinism too, because the adapter polls the same window every
// cycle and a feed that reordered itself between two identical requests would
// make the adapter's cursor meaningless.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// feedPage is one page of the vendor's feed.
type feedPage struct {
	Array      []map[string]any `json:"array"`
	EventCount int              `json:"eventCount"`
	Links      struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
		Next *struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"_links"`
}

// getFeed reads one page of the feed.
//
// The href may be a full path (the continuation is ROOT-RELATIVE, because the
// adapter refuses a next link naming another host: the site's bearer
// credential must not be sent to a host nobody configured), so a relative href
// is resolved against the emulator's own base.
func getFeed(t *testing.T, engine *Engine, query string) feedPage {
	t.Helper()
	url := engine.URL() + APIPrefix + "/events" + query
	if strings.HasPrefix(query, "/") {
		url = engine.URL() + query
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("reach the feed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the feed answered %s, want 200", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the feed: %v", err)
	}
	var page feedPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("parse the feed: %v\n%s", err, body)
	}
	return page
}

// TestTheFeedsContinuationCarriesTheNextPage is the continuation.
//
// The adapter follows `_links.next`, so the link has to be there and it has to
// lead to the events that are on the next page.
func TestTheFeedsContinuationCarriesTheNextPage(t *testing.T) {
	engine := startEmulator(t)
	cam, ok := engine.camera("entrance")
	if !ok {
		t.Fatal("the camera was not created")
	}
	for i := 0; i < 3; i++ {
		engine.publish(cam, []Detection{{
			Class: classPerson, Confidence: 0.9, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.6,
		}})
	}

	first := getFeed(t, engine, "?size=1")
	if len(first.Array) != 1 {
		t.Fatalf("the first page holds %d events, want 1", len(first.Array))
	}
	if first.EventCount < 3 {
		t.Errorf("the feed reports %d events, want at least the 3 that were published", first.EventCount)
	}
	if first.Links.Next == nil {
		t.Fatal("the first page carries no _links.next. The adapter FOLLOWS that link, and " +
			"without it it reads the first page and stops: the events on every page after the " +
			"first are never ingested, silently, with no dead letter")
	}
	if first.Links.Next.Href == "" {
		t.Error("the _links.next carries no href")
	}

	// Follow it, the way the adapter does, until the feed says there is no
	// more. The walk has to END: a link that leads nowhere is a link the
	// adapter follows forever.
	page := first
	seen := 1
	for page.Links.Next != nil {
		if seen > 100 {
			t.Fatal("the feed's continuation never ends")
		}
		page = getFeed(t, engine, page.Links.Next.Href)
		seen++
	}
	if seen < 2 {
		t.Errorf("the walk visited %d page(s), and the feed reported %d events. The "+
			"continuation did not lead anywhere", seen, first.EventCount)
	}
	if len(page.Array) != 1 {
		t.Errorf("the last page holds %d events, want 1", len(page.Array))
	}
	if page.Links.Next != nil {
		t.Error("the last page carries a _links.next. A link that leads nowhere is a link the " +
			"adapter follows forever")
	}
}

// TestTheFeedIsDeterministic is the other half.
//
// The adapter polls the same window every cycle, so two identical requests
// must produce identical bodies. A feed that reordered itself between two
// requests would make the adapter's cursor meaningless.
func TestTheFeedIsDeterministic(t *testing.T) {
	engine := startEmulator(t)
	cam, ok := engine.camera("entrance")
	if !ok {
		t.Fatal("the camera was not created")
	}
	engine.publish(cam, []Detection{{
		Class: classPerson, Confidence: 0.9, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.6,
	}})

	first := getFeed(t, engine, "")
	second := getFeed(t, engine, "")
	if len(first.Array) != len(second.Array) {
		t.Fatalf("the first request held %d events and the second held %d", len(first.Array), len(second.Array))
	}
	for i := range first.Array {
		a, _ := json.Marshal(first.Array[i])
		b, _ := json.Marshal(second.Array[i])
		if string(a) != string(b) {
			t.Errorf("event %d differs between two identical requests:\n  %s\n  %s", i, a, b)
		}
	}
}
