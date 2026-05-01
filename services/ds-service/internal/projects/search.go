package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// Phase 8 U9 — global search handler.
//
// Single SELECT against search_index_fts with FTS5 MATCH and an ACL filter
// that JOINs flow_grants. The query plan uses the FTS5 inverted index for
// the MATCH and a partial index on flow_grants(user_id, flow_id) for the
// EXISTS subquery — both indexed paths.
//
// Auth: Bearer token via the existing AdaptAuthMiddleware. Tenant scoped
// from claims.
//
// Response shape:
//
//	{
//	  "results": [
//	    {"kind": "flow", "id": "flow_abc", "title": "...", "snippet": "...",
//	     "open_url": "/projects/<slug>", "score": -1.42}
//	  ],
//	  "total": 12
//	}
//
// Score is FTS5 rank() — negative because lower (more negative) = better.

// SearchResult is one row of the response payload.
type SearchResult struct {
	Kind    string  `json:"kind"`
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	OpenURL string  `json:"open_url,omitempty"`
	Score   float64 `json:"score"`
}

// SearchResponse is the wire envelope.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
}

// HandleSearch serves GET /v1/search?q=…&limit=…&scope=…
//
// Query params:
//   - q     — required; the FTS5 query string. Special chars (`"`, `*`, etc.)
//             pass through to FTS5 unmodified so power users can write
//             phrase or prefix queries.
//   - limit — optional; clamped to [1, 50]. Default 20.
//   - scope — optional; "all" (default) or "mind-graph". The mind-graph
//             scope intersects results with the user's current
//             `graph_index` slice (Phase 6); see U11 for the in-graph
//             search input that uses this.
func (s *Server) HandleSearch(w http.ResponseWriter, r *http.Request) {
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
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_q", "query required")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			} else if n > 50 {
				n = 50
			}
			limit = n
		}
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "all"
	}

	results, err := s.runSearch(r.Context(), tenantID, claims.Sub, q, scope, limit, isAdmin(claims))
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(SearchResponse{Results: results, Total: len(results)})
}

// runSearch executes the FTS5 + ACL query. Admins (claims.role == admin)
// see every flow's results; non-admins see flows they have grants on OR
// flows where the entity is something other than a flow (decisions /
// personas / components don't get per-flow gated in v1).
//
// SQLite's FTS5 query syntax tolerates user-typed strings; we don't escape
// `*`, `"`, etc. so power users get prefix + phrase support out of the box.
func (s *Server) runSearch(ctx context.Context, tenantID, userID, q, scope string, limit int, admin bool) ([]SearchResult, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if admin {
		rows, err = s.deps.DB.QueryContext(ctx,
			`SELECT s.entity_kind, s.entity_id, s.title,
			        snippet(search_index_fts, 5, '<mark>', '</mark>', '…', 12),
			        s.open_url,
			        rank
			   FROM search_index_fts s
			  WHERE s.tenant_id = ?
			    AND search_index_fts MATCH ?
			  ORDER BY rank
			  LIMIT ?`,
			tenantID, q, limit,
		)
	} else {
		// Non-admin path — flows are gated by flow_grants OR by a
		// "default-role" claim from the user's tenant_users row. v1 keeps
		// it simple: a flow is visible if the user has a grant on it. Non-
		// flow entity kinds are visible to anyone in the tenant.
		rows, err = s.deps.DB.QueryContext(ctx,
			`SELECT s.entity_kind, s.entity_id, s.title,
			        snippet(search_index_fts, 5, '<mark>', '</mark>', '…', 12),
			        s.open_url,
			        rank
			   FROM search_index_fts s
			  WHERE s.tenant_id = ?
			    AND search_index_fts MATCH ?
			    AND (
			        s.entity_kind != 'flow'
			        AND s.entity_kind != 'drd'
			        OR EXISTS (
			            SELECT 1 FROM flow_grants g
			             WHERE g.flow_id = s.entity_id
			               AND g.user_id = ?
			               AND g.revoked_at IS NULL
			        )
			    )
			  ORDER BY rank
			  LIMIT ?`,
			tenantID, q, userID, limit,
		)
	}
	if err != nil {
		// FTS5 syntax errors come back here. Surface a clean 500 — the
		// handler converts to a 4xx upstream if we want, but for v1 we keep
		// the body open.
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	out := make([]SearchResult, 0, limit)
	graphFilter := scope == "mind-graph"
	visibleSet, _ := s.loadGraphVisibleEntities(ctx, tenantID, graphFilter)

	for rows.Next() {
		var (
			kind, id, title, snippet, openURL string
			score                             float64
		)
		if err := rows.Scan(&kind, &id, &title, &snippet, &openURL, &score); err != nil {
			return nil, fmt.Errorf("search scan: %w", err)
		}
		if graphFilter {
			if _, ok := visibleSet[kind+":"+id]; !ok {
				continue
			}
		}
		out = append(out, SearchResult{
			Kind:    kind,
			ID:      id,
			Title:   title,
			Snippet: snippet,
			OpenURL: openURL,
			Score:   score,
		})
	}
	return out, rows.Err()
}

// loadGraphVisibleEntities returns the set of "<kind>:<id>" strings that
// appear in graph_index for the tenant. Used by the mind-graph-scoped
// search filter.
func (s *Server) loadGraphVisibleEntities(ctx context.Context, tenantID string, only bool) (map[string]struct{}, error) {
	if !only {
		return nil, nil
	}
	rows, err := s.deps.DB.QueryContext(ctx,
		`SELECT DISTINCT type, source_ref FROM graph_index WHERE tenant_id = ?`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			continue
		}
		out[kind+":"+id] = struct{}{}
	}
	return out, rows.Err()
}

// isAdmin checks the JWT role claim. Mirrors what existing super-admin
// gating does — Phase 5.2 admin reactivate already uses the same predicate.
func isAdmin(c *auth.Claims) bool {
	if c == nil {
		return false
	}
	return c.Role == "admin" || c.Role == "tenant_admin" || c.Role == "super_admin"
}

// helper exposed for testing — keeps the FTS5 query dance close to the
// handler.
var _ = errors.New
