// Package graph is the M1 Security Graph: topology and provenance only.
//
// The scope is narrow on purpose. M1 builds:
//
//	SITE  - contains   - ZONE
//	ZONE  - contains   - ZONE
//	ZONE  - contains   - DEVICE
//	DEVICE- kind       - camera / door / sensor
//	EVENT - observed_by- DEVICE
//	EVENT - occurred_in- ZONE
//	INCIDENT - supported_by - EVENT
//	INCIDENT - has_evidence - EVIDENCE
//	OBSERVATION - derived_from - EVIDENCE
//	OBSERVATION - about - ENTITY
//
// What is deliberately absent is a person model. There is no identity
// resolution, no cross-camera identity, no re-identification, no behavioural
// graph, no entity aliasing, and no `entity_aliases` table. An entity is a
// concrete thing a device reported - a plate, a badge, a door - and nothing
// further. This is a scope decision, not a missing feature, and the test
// asserting the absence of `entity_aliases` exists to stop it becoming one by
// accident.
//
// The test is `TestNoEntityAliasTableInM1` in `internal/schemalaws`, not in this
// package. It was named here as though it were a test of this package's
// behaviour, which is how a reader ends up looking for a test that does not
// exist and concluding the scope decision is unguarded.
//
// Every edge carries provenance. An edge with no `asserted_by` is refused by the
// schema, because an unattributed edge is exactly the one somebody would want
// to be able to deny.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// NodeType and Relation are the closed vocabularies. M1's graph is small enough
// to enumerate, and an enumeration means an unrecognised relation is a
// deployment fault rather than something to be stored and ignored.
var (
	Relations = map[string]bool{
		"contains":     true,
		"observed_by":  true,
		"occurred_in":  true,
		"supported_by": true,
		"has_evidence": true,
		"derived_from": true,
		"about":        true,
		"kind":         true,
		"revises":      true,
	}
	Methods = map[string]bool{
		"observed": true,
		"asserted": true,
		"inferred": true,
	}
)

// ErrNoProvenance is returned when an edge is offered without saying who
// asserted it.
var ErrNoProvenance = errors.New("graph: an edge must carry provenance")

// Edge is one claim in the graph.
type Edge struct {
	TenantID string
	EdgeID   string
	FromType string
	FromID   string
	Relation string
	ToType   string
	ToID     string
	Provenance
}

// Provenance is the attribution on an edge. Every field here is what makes the
// graph auditable: who asserted it, from what source, when, how confidently, and
// by what method.
type Provenance struct {
	// AssertedBy is the actor or system that made the claim. Required.
	AssertedBy string
	// SourceEventID is the event the claim came from, when it came from one.
	SourceEventID string
	ObservedAt    *time.Time
	// Confidence is 0.0 to 1.0. An asserted topology edge is 1.0; an inferred one
	// is lower, and the number is stored rather than implied by the method.
	Confidence float64
	// Method is observed (a device reported it), asserted (an operator or config
	// declared it) or inferred (the core computed it).
	Method string
	// RuleOrModelID is the rule or model that produced an inferred edge.
	RuleOrModelID string
}

// Validate refuses an edge the graph should not hold.
func (e Edge) Validate() error {
	switch {
	case e.TenantID == "":
		return errors.New("graph: tenant id is required and is never read from a request body")
	case e.FromType == "" || e.FromID == "":
		return errors.New("graph: an edge needs a source node")
	case e.ToType == "" || e.ToID == "":
		return errors.New("graph: an edge needs a target node")
	case !Relations[e.Relation]:
		return fmt.Errorf("graph: relation %q is not one of %s", e.Relation, sortedKeys(Relations))
	case e.AssertedBy == "":
		return fmt.Errorf("%w: from %s/%s %s %s/%s", ErrNoProvenance, e.FromType, e.FromID, e.Relation, e.ToType, e.ToID)
	case !Methods[e.Method]:
		return fmt.Errorf("graph: method %q is not one of %s", e.Method, sortedKeys(Methods))
	case e.Confidence < 0 || e.Confidence > 1:
		return fmt.Errorf("graph: confidence %v is outside 0.0 to 1.0", e.Confidence)
	}
	// An inferred edge without the rule or model that inferred it is a claim
	// with no reasoning, which is the thing the method exists to distinguish.
	if e.Method == "inferred" && e.RuleOrModelID == "" {
		return fmt.Errorf("graph: an inferred edge must name the rule or model that inferred it (%s/%s %s %s/%s)",
			e.FromType, e.FromID, e.Relation, e.ToType, e.ToID)
	}
	return nil
}

// Service writes and reads the graph.
type Service struct {
	Pool *dbx.Pool
}

const insertEdgeSQL = `
INSERT INTO edges (edge_id, tenant_id, from_type, from_id, relation, to_type, to_id,
                   asserted_by, source_event_id, observed_at, confidence, method, rule_or_model_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (tenant_id, from_type, from_id, relation, to_type, to_id) DO NOTHING
RETURNING edge_id`

// AddEdge writes one edge and reports whether it was new.
//
// A duplicate is a no-op, not an error: a replayed event re-asserts edges that
// already exist, and that must be silent rather than an error the caller retries
// for ever.
func (s *Service) AddEdge(ctx context.Context, e Edge) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}
	var id string
	err := s.Pool.For(e.TenantID, "graph").QueryRow(ctx, insertEdgeSQL,
		e.EdgeID, e.TenantID, e.FromType, e.FromID, e.Relation, e.ToType, e.ToID,
		e.AssertedBy, nullString(e.SourceEventID), e.ObservedAt,
		e.Confidence, e.Method, nullString(e.RuleOrModelID)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("graph: add edge: %w", err)
	}
	return true, nil
}

// AddEdges writes a set in one transaction.
//
// Topology comes from a device list, and one transaction per device on a
// 2,000-camera site is 2,000 round trips for a thing that either all happened or
// did not.
func (s *Service) AddEdges(ctx context.Context, edges []Edge) (int, error) {
	if len(edges) == 0 {
		return 0, nil
	}
	for _, e := range edges {
		if err := e.Validate(); err != nil {
			return 0, err
		}
	}
	// One transaction per tenant. A tenant context is a transaction-local setting
	// and a batch may legitimately span tenants, and running one tenant's edges
	// under another tenant's context is the failure this whole path exists to
	// prevent.
	byTenant := map[string][]Edge{}
	order := make([]string, 0, len(edges))
	for _, e := range edges {
		if _, seen := byTenant[e.TenantID]; !seen {
			order = append(order, e.TenantID)
		}
		byTenant[e.TenantID] = append(byTenant[e.TenantID], e)
	}
	added := 0
	for _, tenant := range order {
		batch := byTenant[tenant]
		err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "graph"}, func(tx pgx.Tx) error {
			for _, e := range batch {
				var id string
				err := tx.QueryRow(ctx, insertEdgeSQL,
					e.EdgeID, e.TenantID, e.FromType, e.FromID, e.Relation, e.ToType, e.ToID,
					e.AssertedBy, nullString(e.SourceEventID), e.ObservedAt,
					e.Confidence, e.Method, nullString(e.RuleOrModelID)).Scan(&id)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					return fmt.Errorf("graph: add edge: %w", err)
				}
				added++
			}
			return nil
		})
		if err != nil {
			return added, err
		}
	}
	return added, nil
}

// Revoke retires an edge without deleting it.
//
// A revoked edge stays in the table. Revocation is a historical fact — "this was
// claimed, and then withdrawn" — and deleting the row would make a rebuilt
// projection disagree with the live one. `edges_active` is what traversal reads.
func (s *Service) Revoke(ctx context.Context, tenant, edgeID, by, reason, replacement string) error {
	_, err := s.Pool.For(tenant, "graph").Exec(ctx, `
		INSERT INTO edge_revocations (tenant_id, edge_id, revoked_at, revoked_by, reason, replacement_edge_id)
		VALUES ($1, $2, now(), $3, $4, $5)
		ON CONFLICT (tenant_id, edge_id) DO NOTHING`,
		tenant, edgeID, by, reason, nullString(replacement))
	if err != nil {
		return fmt.Errorf("graph: revoke: %w", err)
	}
	return nil
}

// Node is one thing in the graph, with the labels traversal accumulated for it.
type Node struct {
	Type    string   `json:"type"`
	ID      string   `json:"id"`
	EdgeIDs []string `json:"edgeIds"`
}

// Traversal walks the graph with a recursive CTE.
//
// Bounded twice on purpose: by depth and by node count. A graph with an unbounded
// walk is a graph that can be made to consume the whole database by an event
// naming one node, and an untrusted adapter supplies event names.
type Traversal struct {
	// MaxDepth is the maximum hops. Default 6.
	MaxDepth int
	// MaxNodes caps the result. Default 2,000.
	MaxNodes int
	// FollowedRelations restricts which relations are traversed. Empty means all.
	FollowedRelations []string
}

// Result is a traversal's output.
type Result struct {
	Nodes          []Node   `json:"nodes"`
	EdgeIDs        []string `json:"edgeIds"`
	Truncated      bool     `json:"truncated"`
	VisitedAtDepth int      `json:"maxDepthReached"`
}

const walkSQL = `
WITH RECURSIVE walk(node_type, node_id, edge_id, depth) AS (
    SELECT $2::text, $3::text, ''::text, 0
  UNION
    SELECT e.to_type, e.to_id, e.edge_id, w.depth + 1
      FROM edges_active e
      JOIN walk w ON e.tenant_id = $1
                 AND e.from_type = w.node_type
                 AND e.from_id = w.node_id
     WHERE w.depth < $4
       AND ($5::text[] IS NULL OR e.relation = ANY($5))
)
SELECT node_type, node_id, edge_id, depth FROM walk
 WHERE depth > 0
 ORDER BY depth, node_type, node_id
 LIMIT $6`

// Walk traverses outward from a node.
func (s *Service) Walk(ctx context.Context, tenant, nodeType, nodeID string, t Traversal) (Result, error) {
	var out Result
	if t.MaxDepth <= 0 {
		t.MaxDepth = 6
	}
	if t.MaxNodes <= 0 {
		t.MaxNodes = 2000
	}

	var followed any
	if len(t.FollowedRelations) > 0 {
		for _, r := range t.FollowedRelations {
			if !Relations[r] {
				return out, fmt.Errorf("graph: relation %q is not known", r)
			}
		}
		followed = t.FollowedRelations
	}

	// Through the tenant-scoped pool, like every other read in this file.
	//
	// This one query used the raw pool, and under the role the product runs as
	// that returns zero rows with no error: edges_active is security_invoker
	// (migration 0014), edges has FORCE ROW LEVEL SECURITY, and the policy is
	// tenant_id = app_current_tenant() OR app_is_system_worker(). With no
	// app.tenant_id set, app_current_tenant() is nullif(current_setting(...),'')
	// - NULL, not false - so the predicate is NULL, every row is filtered, and
	// the traversal reports an empty graph. Not an error, not a log line, just
	// nothing. Snapshot and CheckRebuild were already scoped; this was the one
	// that was not, which is why the tests passed: they connect as a superuser,
	// which bypasses RLS entirely.
	rows, err := s.Pool.For(tenant, "graph").Query(ctx, walkSQL, tenant, nodeType, nodeID, t.MaxDepth, followed, t.MaxNodes+1)
	if err != nil {
		return out, fmt.Errorf("graph: walk: %w", err)
	}
	defer rows.Close()

	seenNode := map[string]bool{nodeType + "/" + nodeID: true}
	for rows.Next() {
		var n Node
		var edgeID string
		var depth int
		// node_id and edge_id are separate destinations.
		//
		// They were scanned into `&n.ID` twice, so the edge id overwrote the node
		// id: every `Result.Nodes[].ID` was an edge id, and any caller that
		// traversed from a returned node id got no rows back. The declared
		// `edgeID` stayed empty, so `Result.EdgeIDs` was a list of empty strings.
		// The dedupe below keyed on `type/edgeID`, which is unique per row, so it
		// never fired and the accumulating branch was unreachable.
		if err := rows.Scan(&n.Type, &n.ID, &edgeID, &depth); err != nil {
			return out, fmt.Errorf("graph: scan walk: %w", err)
		}
		if len(out.EdgeIDs) >= t.MaxNodes {
			out.Truncated = true
			break
		}
		out.EdgeIDs = append(out.EdgeIDs, edgeID)
		if depth > out.VisitedAtDepth {
			out.VisitedAtDepth = depth
		}
		key := n.Type + "/" + n.ID
		if !seenNode[key] {
			seenNode[key] = true
			out.Nodes = append(out.Nodes, Node{Type: n.Type, ID: n.ID, EdgeIDs: []string{edgeID}})
		} else {
			for i := range out.Nodes {
				if out.Nodes[i].Type == n.Type && out.Nodes[i].ID == n.ID {
					out.Nodes[i].EdgeIDs = append(out.Nodes[i].EdgeIDs, edgeID)
				}
			}
		}
	}
	return out, rows.Err()
}

// Snapshot is a whole-graph export, for a site whose graph has to be rebuilt
// elsewhere or reviewed offline.
type Snapshot struct {
	TenantID  string    `json:"tenantId"`
	TakenAt   time.Time `json:"takenAt"`
	Edges     []Edge    `json:"edges"`
	Count     int       `json:"count"`
	Truncated bool      `json:"truncated"`
}

// MaxSnapshotEdges bounds a snapshot. A site with 2,000 cameras and a deep zone
// tree produces tens of thousands of edges; the bound is a memory guard, not a
// view.
const MaxSnapshotEdges = 250_000

// Snapshot exports the active graph.
func (s *Service) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	out := Snapshot{TenantID: tenant, TakenAt: time.Now().UTC()}
	rows, err := s.Pool.For(tenant, "graph").Query(ctx, `
		SELECT edge_id, from_type, from_id, relation, to_type, to_id,
		       asserted_by, COALESCE(source_event_id, ''), observed_at, confidence, method,
		       COALESCE(rule_or_model_id, '')
		  FROM edges_active
		 WHERE tenant_id = $1
		 ORDER BY from_type, from_id, relation, to_type, to_id
		 LIMIT $2`, tenant, MaxSnapshotEdges+1)
	if err != nil {
		return out, fmt.Errorf("graph: snapshot: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if len(out.Edges) >= MaxSnapshotEdges {
			out.Truncated = true
			break
		}
		var e Edge
		var source, rule string
		if err := rows.Scan(&e.EdgeID, &e.FromType, &e.FromID, &e.Relation, &e.ToType, &e.ToID,
			&e.AssertedBy, &source, &e.ObservedAt, &e.Confidence, &e.Method, &rule); err != nil {
			return out, fmt.Errorf("graph: scan snapshot: %w", err)
		}
		e.TenantID = tenant
		e.SourceEventID = source
		e.RuleOrModelID = rule
		out.Edges = append(out.Edges, e)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("graph: snapshot rows: %w", err)
	}
	out.Count = len(out.Edges)
	return out, nil
}

// RebuildReport is what a consistency check found about the graph: the edge
// count, and the two ways an edge can cite something that is no longer there.
type RebuildReport struct {
	Edges              int      `json:"edges"`
	OrphanSourceEvents int      `json:"orphanSourceEvents"`
	UnparentedZones    int      `json:"unparentedZones"`
	Problems           []string `json:"problems"`
}

// CheckRebuild inspects the graph's internal consistency and reports what a
// rebuild would need to know. It reports edges whose source event no longer
// exists and zones with no parent; neither is automatically an error - a retained
// incident may outlive its event's partition — but both are facts a rebuild needs.
func (s *Service) CheckRebuild(ctx context.Context, tenant string) (RebuildReport, error) {
	var r RebuildReport

	if err := s.Pool.For(tenant, "graph").QueryRow(ctx, `SELECT count(*) FROM edges_active WHERE tenant_id = $1`, tenant).Scan(&r.Edges); err != nil {
		return r, fmt.Errorf("graph: count edges: %w", err)
	}
	if err := s.Pool.For(tenant, "graph").QueryRow(ctx, `
		SELECT count(*) FROM edges_active e
		 WHERE e.tenant_id = $1
		   AND e.source_event_id IS NOT NULL
		   AND NOT EXISTS (
		       SELECT 1 FROM canonical_events c
		        WHERE c.tenant_id = e.tenant_id AND c.source_event_id = e.source_event_id
		   )`, tenant).Scan(&r.OrphanSourceEvents); err != nil {
		return r, fmt.Errorf("graph: count orphans: %w", err)
	}
	if err := s.Pool.For(tenant, "graph").QueryRow(ctx, `
		SELECT count(*) FROM zones z
		 WHERE z.tenant_id = $1
		   AND EXISTS (SELECT 1 FROM edges_active e
		                WHERE e.tenant_id = z.tenant_id AND e.to_type = 'ZONE' AND e.to_id = z.zone_id)
		   AND NOT EXISTS (SELECT 1 FROM edges_active e
		                    WHERE e.tenant_id = z.tenant_id AND e.from_type = 'ZONE' AND e.to_id = z.zone_id
		                      AND e.relation = 'contains')`,
		tenant).Scan(&r.UnparentedZones); err != nil {
		return r, fmt.Errorf("graph: count unparented zones: %w", err)
	}

	if r.OrphanSourceEvents > 0 {
		r.Problems = append(r.Problems,
			fmt.Sprintf("%d edge(s) cite a source event that is no longer stored; a retained "+
				"incident may outlive its event's retention window, so this needs review rather "+
				"than automatic repair", r.OrphanSourceEvents))
	}
	if r.UnparentedZones > 0 {
		r.Problems = append(r.Problems,
			fmt.Sprintf("%d zone(s) are reachable but have no containing edge", r.UnparentedZones))
	}
	return r, nil
}

// Topology describes a site's shape, which is what the console and the rule
// compiler's zone check need.
type Topology struct {
	Zones   []Zone   `json:"zones"`
	Devices []Device `json:"devices"`
}

// Zone is one node of the hierarchy.
type Zone struct {
	ID       string `json:"id"`
	SiteID   string `json:"siteId"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Depth    int    `json:"depth"`
}

// Device is one registered device.
type Device struct {
	ID     string   `json:"id"`
	SiteID string   `json:"siteId"`
	ZoneID string   `json:"zoneId,omitempty"`
	Kind   string   `json:"kind"`
	Tags   []string `json:"tags,omitempty"`
}

// LoadTopology reads the site's zones and devices.
func (s *Service) LoadTopology(ctx context.Context, tenant string) (Topology, error) {
	var out Topology

	rows, err := s.Pool.For(tenant, "graph").Query(ctx, `SELECT zone_id, site_id, COALESCE(parent_id, ''), name, path, depth
		   FROM zones WHERE tenant_id = $1 ORDER BY path`, tenant)
	if err != nil {
		return out, fmt.Errorf("graph: load zones: %w", err)
	}
	for rows.Next() {
		var z Zone
		if err := rows.Scan(&z.ID, &z.SiteID, &z.ParentID, &z.Name, &z.Path, &z.Depth); err != nil {
			rows.Close()
			return out, fmt.Errorf("graph: scan zone: %w", err)
		}
		out.Zones = append(out.Zones, z)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	drows, err := s.Pool.For(tenant, "graph").Query(ctx, `SELECT device_id, site_id, COALESCE(zone_id, ''), kind
		   FROM devices WHERE tenant_id = $1 AND enabled
		 ORDER BY device_id`, tenant)
	if err != nil {
		return out, fmt.Errorf("graph: load devices: %w", err)
	}
	defer drows.Close()
	for drows.Next() {
		var d Device
		if err := drows.Scan(&d.ID, &d.SiteID, &d.ZoneID, &d.Kind); err != nil {
			return out, fmt.Errorf("graph: scan device: %w", err)
		}
		out.Devices = append(out.Devices, d)
	}
	return out, drows.Err()
}

// ZonePaths returns the site's zone path set, which is what the rule compiler
// checks a `zone.pathPrefix` against.
func (s *Service) ZonePaths(ctx context.Context, tenant string) (map[string]bool, error) {
	rows, err := s.Pool.For(tenant, "graph").Query(ctx, `SELECT path FROM zones WHERE tenant_id = $1`, tenant)
	if err != nil {
		return nil, fmt.Errorf("graph: load zone paths: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("graph: scan zone path: %w", err)
		}
		out[p] = true
	}
	return out, rows.Err()
}

// MarshalJSON renders a snapshot in a stable form, so two exports of the
// same graph are byte-identical and a diff means something.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type alias Snapshot
	return json.Marshal(struct {
		alias
		Edges []Edge `json:"edges"`
	}{alias(s), s.Edges})
}

func nullString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
