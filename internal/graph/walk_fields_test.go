package graph_test

import (
	"testing"

	"github.com/Sergey-Bar/vision/internal/graph"
)

// `Walk` scanned `node_id` and `edge_id` into the same destination, so the edge
// id overwrote the node id.
//
// Every `Result.Nodes[].ID` was an edge id, and a caller that traversed from a
// returned node id — which is the obvious thing to do with a traversal result —
// got no rows back. The declared `edgeID` stayed empty, so `Result.EdgeIDs` was a
// list of empty strings, and the dedupe keyed on `type/edgeID`, which is unique
// per row, so it never fired and the accumulating branch was unreachable.
func TestWalkReturnsNodeIDsAndEdgeIDsInTheirOwnFields(t *testing.T) {
	s, tenant, site := service(t)
	ctx := t.Context()
	_ = site

	if _, err := s.AddEdges(ctx, []graph.Edge{
		{TenantID: tenant, EdgeID: "a-b", FromType: "SITE", FromID: site, Relation: "contains",
			ToType: "ZONE", ToID: "zone-entrance", Provenance: proven()},
		{TenantID: tenant, EdgeID: "b-c", FromType: "ZONE", FromID: "zone-entrance", Relation: "contains",
			ToType: "DEVICE", ToID: "cam-1", Provenance: proven()},
	}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	res, err := s.Walk(ctx, tenant, "SITE", site, graph.Traversal{MaxDepth: 3})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("the walk returned %d node(s), want 2: %+v", len(res.Nodes), res.Nodes)
	}

	byID := map[string]graph.Node{}
	for _, n := range res.Nodes {
		byID[n.ID] = n
	}
	// The node ids are the thing a caller traverses from, and they must be the
	// node ids.
	for _, want := range []struct{ typ, id string }{
		{"ZONE", "zone-entrance"},
		{"DEVICE", "cam-1"},
	} {
		n, ok := byID[want.id]
		if !ok {
			t.Errorf("the walk did not return node %s/%s. It returned %+v, whose ids are the "+
				"edge ids: Walk scanned node_id and edge_id into one destination",
				want.typ, want.id, res.Nodes)
			continue
		}
		if n.Type != want.typ {
			t.Errorf("node %s has type %q, want %q", want.id, n.Type, want.typ)
		}
	}

	// And the edge ids are in their own field, not blank.
	for _, id := range res.EdgeIDs {
		if id == "" {
			t.Errorf("the walk returned an empty edge id: %v. `edgeID` was declared, never "+
				"assigned, and every entry of EdgeIDs was the empty string", res.EdgeIDs)
			break
		}
	}

	// And the result is usable: traversing from a returned node id returns rows.
	// This is the thing the bug actually broke, and it is the natural thing for
	// a caller to do with a traversal.
	if res.Nodes[0].ID == "" {
		t.Fatal("a returned node has no id")
	}
	again, err := s.Walk(ctx, tenant, res.Nodes[0].Type, res.Nodes[0].ID, graph.Traversal{MaxDepth: 1})
	if err != nil {
		t.Fatalf("traversing from a returned node: %v", err)
	}
	if len(again.EdgeIDs) == 0 {
		t.Error("traversing from an id the walk itself returned found nothing. The id it " +
			"returned is not a node id")
	}
}

func proven() graph.Provenance {
	return graph.Provenance{AssertedBy: "adapter:milestone", Method: "asserted", Confidence: 1.0}
}
