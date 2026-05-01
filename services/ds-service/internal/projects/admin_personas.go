package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// Phase 7 U4 — persona library approval queue.
//
// Designer-suggested personas land in the personas table with status='pending'
// (Phase 1 schema). DS-leads review them on /atlas/admin/personas; approval
// flips status='approved'; rejection soft-deletes via deleted_at.
//
// SSE: a `persona.pending` event fires on the inbox:<tenant_id> channel
// when a designer creates a new pending persona — the admin's bell
// reflects the new entry without polling. The publish call lives at the
// site that creates the persona (Phase 1 plugin export); we just provide
// the read handlers here.

// PendingPersonaRow is the read shape for the admin queue.
type PendingPersonaRow struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	CreatedByUserID    string `json:"created_by_user_id"`
	CreatedByEmail     string `json:"created_by_email,omitempty"`
	CreatedAt          string `json:"created_at"`
}

// listPendingPersonas reads every status='pending' persona, joined to
// users for the email display. Org-wide (no tenant_id on personas) — but
// admin gating is per-tenant (super-admin claim).
func listPendingPersonas(ctx context.Context, db *sql.DB) ([]PendingPersonaRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT p.id, p.name, p.created_by_user_id, COALESCE(u.email, ''), p.created_at
		   FROM personas p
		   LEFT JOIN users u ON u.id = p.created_by_user_id
		  WHERE p.status = 'pending' AND p.deleted_at IS NULL
		  ORDER BY p.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list pending personas: %w", err)
	}
	defer rows.Close()
	var out []PendingPersonaRow
	for rows.Next() {
		var r PendingPersonaRow
		if err := rows.Scan(&r.ID, &r.Name, &r.CreatedByUserID, &r.CreatedByEmail, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending persona: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// approvePersona flips a persona from pending → approved. Idempotent for
// already-approved rows (returns ErrNotFound when nothing matches because
// the row is either deleted or already approved). Sets approved_by +
// approved_at.
func approvePersona(ctx context.Context, db *sql.DB, personaID, approverID string, now time.Time) error {
	res, err := db.ExecContext(ctx,
		`UPDATE personas
		    SET status = 'approved',
		        approved_by_user_id = ?,
		        approved_at = ?
		  WHERE id = ? AND status = 'pending' AND deleted_at IS NULL`,
		approverID, rfc3339(now.UTC()), personaID,
	)
	if err != nil {
		return fmt.Errorf("approve persona: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// rejectPersona soft-deletes a pending persona. The row stays for audit;
// it's just hidden from the live queue.
func rejectPersona(ctx context.Context, db *sql.DB, personaID string, now time.Time) error {
	res, err := db.ExecContext(ctx,
		`UPDATE personas
		    SET deleted_at = ?
		  WHERE id = ? AND status = 'pending' AND deleted_at IS NULL`,
		rfc3339(now.UTC()), personaID,
	)
	if err != nil {
		return fmt.Errorf("reject persona: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// HandleAdminListPendingPersonas serves GET /v1/atlas/admin/personas/pending.
func (s *Server) HandleAdminListPendingPersonas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if _, ok := s.requireAdminTenant(w, r); !ok {
		return
	}
	rows, err := listPendingPersonas(r.Context(), s.deps.DB.DB)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": rows})
}

// HandleAdminApprovePersona serves POST /v1/atlas/admin/personas/{id}/approve.
func (s *Server) HandleAdminApprovePersona(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if _, ok := s.requireAdminTenant(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_id", "")
		return
	}
	if err := approvePersona(r.Context(), s.deps.DB.DB, id, claims.Sub, time.Now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_pending", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "approve_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleAdminRejectPersona serves POST /v1/atlas/admin/personas/{id}/reject.
func (s *Server) HandleAdminRejectPersona(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if _, ok := s.requireAdminTenant(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_id", "")
		return
	}
	if err := rejectPersona(r.Context(), s.deps.DB.DB, id, time.Now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_pending", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "reject_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
