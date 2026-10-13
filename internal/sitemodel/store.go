package sitemodel

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// Service persists the adjacency a site model derives.
//
// The table is a projection: the operator's YAML is the fact, and
// `camera_adjacency` is the derivable view of it the product queries. That is
// why `replaceFromModel` rewrites by site rather than appending — a row the
// model no longer states is a row that should not survive the reload, and a
// projection that accumulates stale facts is worse than one that recomputes.
type Service struct {
	Pool *dbx.Pool
}

// ModelSource and operatorSource are the row provenance values. A row an
// operator asserted by hand is one the model does not state, and a reload
// keeps it: the model is a projection of what the operator measured, not a
// replacement for what they know.
const (
	ModelSource    = "site_model"
	operatorSource = "operator"
)

// replaceSQL deletes the site's model-derived rows, leaving the operator's
// own. Deleting first is what makes the recompute idempotent: the same model
// loaded twice produces the same rows, and a camera removed from a zone stops
// being its neighbour without a second code path to notice.
const replaceSQL = `
DELETE FROM camera_adjacency
 WHERE tenant_id = $1 AND site_id = $2 AND source = 'site_model'`

const insertSQL = `
INSERT INTO camera_adjacency
    (tenant_id, site_id, camera_id, neighbour_id, relation, source, model_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING`

// ReplaceFromModel derives the model's adjacency and rewrites the site's rows.
//
// The whole thing runs in one transaction with the caller's tenant context, so
// a reader between the delete and the insert sees either the old projection or
// the new one and never a site with no camera neighbours — which would read as
// "these cameras have nothing to do with each other", a claim nobody made.
func (s *Service) ReplaceFromModel(ctx context.Context, tenantID, siteID, modelVersion string, model *Site) error {
	if s == nil || s.Pool == nil {
		return errors.New("sitemodel: no pool")
	}
	if siteID == "" {
		return errors.New("sitemodel: site id is required")
	}
	if model == nil {
		return errors.New("sitemodel: no model")
	}
	return s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenantID, ActorID: "sitemodel"}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, replaceSQL, tenantID, siteID); err != nil {
			return fmt.Errorf("sitemodel: clear derived adjacency: %w", err)
		}
		for _, adj := range model.AdjacentCameras() {
			if _, err := tx.Exec(ctx, insertSQL,
				tenantID, siteID, adj.CameraID, adj.NeighbourID, adj.Relation,
				ModelSource, modelVersion); err != nil {
				return fmt.Errorf("sitemodel: record adjacency %s~%s: %w",
					adj.CameraID, adj.NeighbourID, err)
			}
		}
		return nil
	})
}

// neighboursSQL answers "which cameras are adjacent to this one", in either
// direction of the stored pair, because the pair is stored once and the
// question is symmetric.
const neighboursSQL = `
SELECT CASE WHEN camera_id = $3 THEN neighbour_id ELSE camera_id END AS neighbour
  FROM camera_adjacency
 WHERE tenant_id = $1 AND site_id = $2
   AND (camera_id = $3 OR neighbour_id = $3)
 ORDER BY neighbour`

// Neighbours returns the ids adjacent to one camera, deduplicated: a camera
// related by two relations at once is one neighbour.
func (s *Service) Neighbours(ctx context.Context, tenantID, siteID, cameraID string) ([]string, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("sitemodel: no pool")
	}
	rows, err := s.Pool.For(tenantID, "sitemodel").Query(ctx, neighboursSQL, tenantID, siteID, cameraID)
	if err != nil {
		return nil, fmt.Errorf("sitemodel: neighbours of %s: %w", cameraID, err)
	}
	defer rows.Close()
	var out []string
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sitemodel: scan neighbour: %w", err)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sitemodel: iterate neighbours: %w", err)
	}
	return out, nil
}
