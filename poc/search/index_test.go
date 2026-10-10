package search

import (
	"testing"
	"time"
)

func TestTokenSearchMatchesAcrossFields(t *testing.T) {
	idx := NewIndex(time.Hour, 50)
	defer idx.Stop()

	idx.Add(Entry{
		ID:        "f1",
		Timestamp: time.Now().UTC(),
		Categories: []CategoryInfo{
			{Category: "vehicle", SubCategory: "car", Label: "red sedan", Confidence: 0.9},
			{Category: "biological", SubCategory: "human", Label: "person", Confidence: 0.8},
		},
	})
	idx.Add(Entry{
		ID:        "f2",
		Timestamp: time.Now().UTC(),
		Categories: []CategoryInfo{
			{Category: "biological", SubCategory: "dog", Label: "dog", Confidence: 0.7},
		},
	})

	cases := []struct {
		query string
		want  int
	}{
		{"red car", 1}, // token AND across label ("red") and sub-category ("car")
		{"person", 1},  // single token, direct label hit
		{"dog", 1},     // second entry
		{"red dog", 0}, // tokens split across entries must not match either
		{"purple", 0},  // absent token
		{"", 2},        // empty query returns everything
	}
	for _, tc := range cases {
		got := idx.Search(Query{Text: tc.query, Limit: 10})
		if got.Total != tc.want {
			t.Errorf("Search(%q) total = %d, want %d", tc.query, got.Total, tc.want)
		}
	}
}

func TestSearchRespectsLimitAndConfidence(t *testing.T) {
	idx := NewIndex(time.Hour, 50)
	defer idx.Stop()

	for i := 0; i < 5; i++ {
		idx.Add(Entry{
			ID:        "f" + string(rune('a'+i)),
			Timestamp: time.Now().UTC(),
			Categories: []CategoryInfo{
				{Category: "vehicle", Label: "car", Confidence: float64(i) / 10},
			},
		})
	}

	if got := idx.Search(Query{Text: "car", Limit: 2}); got.Total != 2 {
		t.Errorf("limit: total = %d, want 2", got.Total)
	}
	if got := idx.Search(Query{Text: "car", MinConfidence: 0.3}); got.Total != 2 {
		t.Errorf("min confidence: total = %d, want 2", got.Total)
	}
}
