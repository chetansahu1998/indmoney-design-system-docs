package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// personas.go — non-admin persona list endpoint.
//
// The Figma plugin (and the atlas inspector's persona-filter chips) need
// the list of approved personas for the caller's tenant so designers can
// pick from the canonical set instead of inventing names. Admin endpoints
// already exist for the approval queue (admin_personas.go); this file
// adds the regular list path.

// PersonaRow is the wire shape returned by /v1/personas.
type PersonaRow struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	CreatedByUserID    string `json:"created_by_user_id"`
	ApprovedByUserID   string `json:"approved_by_user_id,omitempty"`
	ApprovedAt         string `json:"approved_at,omitempty"`
	CreatedAt          string `json:"created_at"`
}

// HandleListPersonas serves GET /v1/personas?status=approved|pending|all
//
// Auth: any authenticated user in the tenant.
// Default `status` filter: approved (the common case for selecting from
// in the plugin / atlas chips). Pending and "all" are useful for admin UI
// without forcing it through the super-admin gate.
func (s *Server) HandleListPersonas(w http.ResponseWriter, r *http.Request) {
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
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "approved"
	}
	if status != "approved" && status != "pending" && status != "all" {
		writeJSONErr(w, http.StatusBadRequest, "invalid_status",
			"status must be 'approved', 'pending', or 'all'")
		return
	}

	rows, err := listPersonasForTenant(r.Context(), s.deps.DB.DB, tenantID, status)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_personas", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{
		"personas": rows,
		"count":    len(rows),
		"status":   status,
	})
}

func listPersonasForTenant(ctx context.Context, db *sql.DB, tenantID, status string) ([]PersonaRow, error) {
	if tenantID == "" {
		return nil, errors.New("personas: tenant_id required")
	}
	var (
		query string
		args  []any
	)
	if status == "all" {
		query = `SELECT id, name, status, created_by_user_id,
		                COALESCE(approved_by_user_id, ''), COALESCE(approved_at, ''),
		                created_at
		           FROM personas
		          WHERE tenant_id = ? AND deleted_at IS NULL
		          ORDER BY status, name`
		args = []any{tenantID}
	} else {
		query = `SELECT id, name, status, created_by_user_id,
		                COALESCE(approved_by_user_id, ''), COALESCE(approved_at, ''),
		                created_at
		           FROM personas
		          WHERE tenant_id = ? AND status = ? AND deleted_at IS NULL
		          ORDER BY name`
		args = []any{tenantID, status}
	}

	r, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query personas: %w", err)
	}
	defer r.Close()
	var out []PersonaRow
	for r.Next() {
		var p PersonaRow
		if err := r.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedByUserID, &p.ApprovedByUserID, &p.ApprovedAt, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan persona: %w", err)
		}
		out = append(out, p)
	}
	return out, r.Err()
}

// Compile-time guard.
var _ http.HandlerFunc = (*Server)(nil).HandleListPersonas

// silence unused import (json) when the file is the only consumer in the package.
var _ = json.Marshal
