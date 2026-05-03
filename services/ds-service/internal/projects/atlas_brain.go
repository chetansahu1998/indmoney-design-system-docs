package projects

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// atlas_brain.go — Phase 9 (atlas-shell port).
//
// Backs the brain-graph view at /atlas. Each row is one project rolled up to
// the counts the UI needs to render a single node:
//   - screen_count   → drives the dot size / "primary" flag
//   - active_violations → drives the severity halo around the node
//   - flow_count     → drives "leaves available" affordance in the inspector
//   - latest_version_id → handed to the leaf canvas as the active version
//
// One indexed query per request; aggregates inline so we do not N+1 across
// projects. Caps at 500 rows to match ListProjects.

// AtlasBrainNode is the wire shape the /atlas brain consumes per project.
type AtlasBrainNode struct {
	ID                string `json:"id"`
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	Platform          string `json:"platform"`
	Product           string `json:"product"`
	Path              string `json:"path"`
	UpdatedAt         string `json:"updated_at"`
	LatestVersionID   string `json:"latest_version_id,omitempty"`
	ScreenCount       int    `json:"screen_count"`
	FlowCount         int    `json:"flow_count"`
	ActiveViolations  int    `json:"active_violations"`
}

// ListAtlasBrainNodes runs ONE query that joins each project with its newest
// view_ready version and rolls up screen / violation / flow counts. Projects
// with no view_ready version still appear (LEFT JOIN), but with zeroed
// counts — the UI renders them as ghost nodes so users see in-flight work.
func (t *TenantRepo) ListAtlasBrainNodes(ctx context.Context, platform string, limit int) ([]AtlasBrainNode, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if platform != GraphPlatformMobile && platform != GraphPlatformWeb {
		return nil, fmt.Errorf("projects: invalid platform %q", platform)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	const q = `
WITH latest_version AS (
  SELECT pv.project_id, pv.id AS version_id, pv.version_index
    FROM project_versions pv
   WHERE pv.tenant_id = ?
     AND pv.status   = 'view_ready'
     AND pv.version_index = (
           SELECT MAX(p2.version_index)
             FROM project_versions p2
            WHERE p2.tenant_id = pv.tenant_id
              AND p2.project_id = pv.project_id
              AND p2.status     = 'view_ready'
         )
)
SELECT p.id, p.slug, p.name, p.platform, p.product, p.path, p.updated_at,
       COALESCE(lv.version_id, '') AS version_id,
       COALESCE((SELECT COUNT(*) FROM screens    s WHERE s.tenant_id = p.tenant_id AND s.version_id = lv.version_id), 0) AS screen_count,
       COALESCE((SELECT COUNT(*) FROM violations v WHERE v.tenant_id = p.tenant_id AND v.version_id = lv.version_id AND v.status = 'active'), 0) AS active_violations,
       COALESCE((SELECT COUNT(*) FROM flows     f WHERE f.tenant_id = p.tenant_id AND f.project_id = p.id AND f.deleted_at IS NULL), 0) AS flow_count
  FROM projects p
  LEFT JOIN latest_version lv ON lv.project_id = p.id
 WHERE p.tenant_id = ?
   AND p.deleted_at IS NULL
   AND p.platform   = ?
 ORDER BY p.updated_at DESC
 LIMIT ?`

	rows, err := t.r.db.QueryContext(ctx, q, t.tenantID, t.tenantID, platform, limit)
	if err != nil {
		return nil, fmt.Errorf("query atlas brain nodes: %w", err)
	}
	defer rows.Close()

	var out []AtlasBrainNode
	for rows.Next() {
		var (
			n         AtlasBrainNode
			updatedAt string
		)
		if err := rows.Scan(
			&n.ID, &n.Slug, &n.Name, &n.Platform, &n.Product, &n.Path, &updatedAt,
			&n.LatestVersionID, &n.ScreenCount, &n.ActiveViolations, &n.FlowCount,
		); err != nil {
			return nil, fmt.Errorf("scan atlas brain node: %w", err)
		}
		// Keep the raw RFC3339 string — the frontend formats relative time
		// itself and will reject Date.parse on missing tz suffixes only on
		// fallback; storing the bare DB value matches every other handler.
		n.UpdatedAt = updatedAt
		out = append(out, n)
	}
	return out, rows.Err()
}

// HandleAtlasBrainNodes serves GET /v1/projects/atlas/brain-nodes?platform=mobile|web.
//
// Returns the rolled-up project list the brain-graph consumes. No SSE here —
// callers re-fetch on the existing graph:<tenant>:<platform> bust channel.
//
// Cache-Control: private, max-age=15. The aggregates change whenever any
// project version flips to view_ready, but the GraphIndexUpdated event already
// triggers a refetch, so a small client cache window is safe.
func (s *Server) HandleAtlasBrainNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return
	}
	platform := r.URL.Query().Get("platform")
	if platform != GraphPlatformMobile && platform != GraphPlatformWeb {
		writeJSONErr(w, http.StatusBadRequest, "invalid_platform",
			"platform must be 'mobile' or 'web'")
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	nodes, err := repo.ListAtlasBrainNodes(r.Context(), platform, limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "atlas_brain_nodes", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=15")
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":     nodes,
		"count":     len(nodes),
		"platform":  platform,
		"limit":     limit,
		"truncated": len(nodes) == limit,
	})
}

// Compile-time guard: HandleAtlasBrainNodes is a stdlib http.Handler.
var _ http.HandlerFunc = (*Server)(nil).HandleAtlasBrainNodes
