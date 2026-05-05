package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// ─── HTTP handlers for screen_text_overrides (U2 of plan 2026-05-05-002) ────
//
// Body shape, MaxBytesReader cap, and 409-on-revision-conflict mirror
// HandlePutDRD exactly so the frontend save-state machine is reusable.

// MaxOverrideValueBytes caps the PUT body for a single override. Override
// values are short strings (UI labels, microcopy), never long-form content;
// 16 KB leaves headroom for legitimate copy + JSON wrapper.
const MaxOverrideValueBytes = 16 * 1024

// MaxOverrideBulkRows is the per-request cap for HandleBulkUpsertOverrides.
// Mirrors MaxBulkLifecycleRows from Phase 4 for consistency.
const MaxOverrideBulkRows = 100

// MaxOverrideBulkBodyBytes covers 100 rows * 16 KB value + ~512 B framing
// per row. Slight headroom for unicode-heavy bodies. Anything past this →
// 413 (don't even try to parse).
const MaxOverrideBulkBodyBytes = (MaxOverrideValueBytes + 1024) * (MaxOverrideBulkRows + 1)

// putOverrideRequest is the JSON body for PUT
// /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}.
type putOverrideRequest struct {
	Value                string `json:"value"`
	ExpectedRevision     int    `json:"expected_revision"`
	CanonicalPath        string `json:"canonical_path"`
	LastSeenOriginalText string `json:"last_seen_original_text"`
}

// bulkUpsertOverridesRequest mirrors the shape POST'd by U12's CSV import +
// future bulk callers. Each row is one override; up to MaxOverrideBulkRows.
type bulkUpsertOverridesRequest struct {
	Items []bulkOverrideItem `json:"items"`
}

type bulkOverrideItem struct {
	ScreenID             string `json:"screen_id"`
	FigmaNodeID          string `json:"figma_node_id"`
	Value                string `json:"value"`
	CanonicalPath        string `json:"canonical_path"`
	LastSeenOriginalText string `json:"last_seen_original_text"`
}

// HandleListOverrides serves both
//   GET /v1/projects/{slug}/screens/{id}/text-overrides
//   GET /v1/projects/{slug}/leaves/{leaf_id}/text-overrides
//
// The router dispatches both paths to the same handler; it picks which list
// path to use by checking which path-value is set.
func (s *Server) HandleListOverrides(w http.ResponseWriter, r *http.Request) {
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
	slug := r.PathValue("slug")
	screenID := r.PathValue("id")
	leafID := r.PathValue("leaf_id")
	if slug == "" || (screenID == "" && leafID == "") {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	var (
		out []ScreenTextOverride
		err error
	)
	if screenID != "" {
		out, err = repo.ListOverridesByScreen(r.Context(), slug, screenID)
	} else {
		out, err = repo.ListOverridesByLeaf(r.Context(), slug, leafID)
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_overrides", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"overrides": out,
	})
}

// HandlePutOverride serves PUT
// /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}.
//
// Body capped at MaxOverrideValueBytes (16 KB); oversize → 413.
// 409 on stale expected_revision exactly like HandlePutDRD.
func (s *Server) HandlePutOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT only")
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
	slug := r.PathValue("slug")
	screenID := r.PathValue("id")
	figmaNodeID := r.PathValue("figma_node_id")
	if slug == "" || screenID == "" || figmaNodeID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxOverrideValueBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			writeJSONErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("override body exceeds %d bytes", MaxOverrideValueBytes))
			return
		}
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}

	var req putOverrideRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.ExpectedRevision < 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid_revision",
			"expected_revision must be >= 0")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	endpoint := fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s",
		slug, screenID, figmaNodeID)
	auditFn := func(tx *sql.Tx, flowID string, newRev int) error {
		details, _ := json.Marshal(map[string]any{
			"flow_id":         flowID,
			"screen_id":       screenID,
			"figma_node_id":   figmaNodeID,
			"canonical_path":  req.CanonicalPath,
			"value":           req.Value,
			"revision":        newRev,
			"schema_ver":      ProjectsSchemaVersion,
		})
		_, err := tx.ExecContext(r.Context(),
			`INSERT INTO audit_log
			    (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(),
			time.Now().UTC().Format(time.RFC3339Nano),
			"override.text.set",
			tenantID,
			claims.Sub,
			"PUT",
			endpoint,
			http.StatusOK,
			0,
			clientIP(r),
			string(details),
		)
		return err
	}

	res, err := repo.UpsertOverride(r.Context(), OverrideUpsertInput{
		ProjectSlug:          slug,
		ScreenID:             screenID,
		FigmaNodeID:          figmaNodeID,
		Value:                req.Value,
		CanonicalPath:        req.CanonicalPath,
		LastSeenOriginalText: req.LastSeenOriginalText,
		ExpectedRevision:     req.ExpectedRevision,
		UpdatedByUserID:      claims.Sub,
	}, auditFn)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if errors.Is(err, ErrRevisionConflict) {
		// Look up the live row so the client can render a diff. Read happens
		// outside the failed write tx — fine, the conflict already means
		// someone else committed.
		current := s.lookupOverrideForConflict(r.Context(), repo, screenID, figmaNodeID)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":            "revision_conflict",
			"current_revision": current.Revision,
			"current_value":    current.Value,
		})
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "override_upsert", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"revision":   res.Revision,
		"updated_at": res.UpdatedAt.Format(time.RFC3339),
	})
}

// HandleDeleteOverride serves DELETE
// /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}.
//
// Idempotent: missing rows return 204 too. On a real delete we emit one
// `override.text.reset` audit_log row inside the same transaction.
func (s *Server) HandleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE only")
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
	slug := r.PathValue("slug")
	screenID := r.PathValue("id")
	figmaNodeID := r.PathValue("figma_node_id")
	if slug == "" || screenID == "" || figmaNodeID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	endpoint := fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s",
		slug, screenID, figmaNodeID)
	auditFn := func(tx *sql.Tx, flowID string) error {
		details, _ := json.Marshal(map[string]any{
			"flow_id":       flowID,
			"screen_id":     screenID,
			"figma_node_id": figmaNodeID,
			"schema_ver":    ProjectsSchemaVersion,
		})
		_, err := tx.ExecContext(r.Context(),
			`INSERT INTO audit_log
			    (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(),
			time.Now().UTC().Format(time.RFC3339Nano),
			"override.text.reset",
			tenantID,
			claims.Sub,
			"DELETE",
			endpoint,
			http.StatusNoContent,
			0,
			clientIP(r),
			string(details),
		)
		return err
	}

	_, _, err := repo.DeleteOverride(r.Context(), slug, screenID, figmaNodeID, auditFn)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "override_delete", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleBulkUpsertOverrides serves POST
// /v1/projects/{slug}/text-overrides/bulk.
//
// Accepts up to MaxOverrideBulkRows items in one transaction. Per-row
// audit_log rows share a single `bulk_id` so log queries can re-aggregate
// the bulk operation. Last-write-wins per row (no expected_revision in bulk
// mode) — bulk callers (CSV import) would otherwise have to fetch every
// row's current revision first which would defeat the bulk endpoint's
// purpose.
func (s *Server) HandleBulkUpsertOverrides(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
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
	slug := r.PathValue("slug")
	if slug == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_slug", "")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxOverrideBulkBodyBytes))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req bulkUpsertOverridesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if len(req.Items) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "items required")
		return
	}
	if len(req.Items) > MaxOverrideBulkRows {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload",
			fmt.Sprintf("max %d items per request", MaxOverrideBulkRows))
		return
	}
	for i, it := range req.Items {
		if it.ScreenID == "" || it.FigmaNodeID == "" {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload",
				fmt.Sprintf("items[%d]: screen_id + figma_node_id required", i))
			return
		}
		if len(it.Value) > MaxOverrideValueBytes {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload",
				fmt.Sprintf("items[%d]: value exceeds %d bytes", i, MaxOverrideValueBytes))
			return
		}
	}

	bulkID := uuid.NewString()
	endpoint := fmt.Sprintf("/v1/projects/%s/text-overrides/bulk", slug)
	rows := make([]*BulkOverrideRow, 0, len(req.Items))
	for _, it := range req.Items {
		// Capture loop vars in the closure.
		item := it
		row := &BulkOverrideRow{
			ScreenID:             item.ScreenID,
			FigmaNodeID:          item.FigmaNodeID,
			Value:                item.Value,
			CanonicalPath:        item.CanonicalPath,
			LastSeenOriginalText: item.LastSeenOriginalText,
		}
		row.PerRowAudit = func(tx *sql.Tx, flowID string, newRev int) error {
			details, _ := json.Marshal(map[string]any{
				"flow_id":        flowID,
				"screen_id":      item.ScreenID,
				"figma_node_id":  item.FigmaNodeID,
				"canonical_path": item.CanonicalPath,
				"value":          item.Value,
				"revision":       newRev,
				"bulk_id":        bulkID,
				"schema_ver":     ProjectsSchemaVersion,
			})
			_, err := tx.ExecContext(r.Context(),
				`INSERT INTO audit_log
				    (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid.NewString(),
				time.Now().UTC().Format(time.RFC3339Nano),
				"override.text.set",
				tenantID,
				claims.Sub,
				"POST",
				endpoint,
				http.StatusOK,
				0,
				clientIP(r),
				string(details),
			)
			return err
		}
		rows = append(rows, row)
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	summary, err := repo.BulkUpsertOverrides(r.Context(), slug, rows, claims.Sub)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "bulk_upsert", err.Error())
		return
	}

	results := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		// Skipped rows have Revision == 0 and FlowID == "".
		if row.FlowID == "" {
			continue
		}
		results = append(results, map[string]any{
			"screen_id":     row.ScreenID,
			"figma_node_id": row.FigmaNodeID,
			"revision":      row.Revision,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bulk_id": bulkID,
		"updated": summary.Updated,
		"skipped": summary.Skipped,
		"results": results,
	})
}

// lookupOverrideForConflict fetches just enough of the current row to
// surface in a 409 body. Best-effort — if the lookup itself fails we
// return zero values rather than masking the original conflict.
func (s *Server) lookupOverrideForConflict(ctx context.Context, repo *TenantRepo, screenID, figmaNodeID string) ScreenTextOverride {
	var out ScreenTextOverride
	_ = repo.handle().QueryRowContext(ctx,
		`SELECT id, screen_id, figma_node_id, canonical_path,
		        last_seen_original_text, value, revision, status,
		        updated_by_user_id, updated_at
		   FROM screen_text_overrides
		  WHERE screen_id = ? AND figma_node_id = ? AND tenant_id = ?
		  LIMIT 1`,
		screenID, figmaNodeID, repo.tenantID,
	).Scan(&out.ID, &out.ScreenID, &out.FigmaNodeID, &out.CanonicalPath,
		&out.LastSeenOriginalText, &out.Value, &out.Revision, &out.Status,
		&out.UpdatedByUserID, &out.UpdatedAt)
	return out
}
