package projects

import (
	"context"
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

// server_figma_promote.go — U5 of the Phase 2 plan
// (docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md).
//
//	POST /v1/admin/figma-inventory/files/{file_key}/promote
//
// Creates (or returns) a DS-internal `projects` row linked to a Figma
// file already mirrored in the FIGMA DB inventory. The bridge between
// the inventory the poller maintains (figma_team_seed → figma_project →
// figma_file) and the audit pipeline's existing project surface
// (projects + project_versions + flows + screens), all keyed on the
// same Figma file_key.
//
// Idempotent: re-promoting the same file_key returns the existing row
// with `created: false`. The existing UpsertProject path (T5 in
// repository.go) already resolves on `(tenant_id, file_id)`, so this
// handler is mostly metadata mapping + an audit row.

// HandleFigmaInventoryPromote serves the promote endpoint.
//
// Body (all optional): {"platform":"web|mobile"}. Defaults to "web".
func (s *Server) HandleFigmaInventoryPromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	tenantID, ok := s.requireAdminTenant(w, r)
	if !ok {
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)

	fileKey := r.PathValue("file_key")
	if strings.TrimSpace(fileKey) == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_file_key", "file_key path param required")
		return
	}

	// Body is optional — only platform is configurable for now. Caps the
	// body small because we're reading at most a few JSON fields.
	type promoteReq struct {
		Platform string `json:"platform,omitempty"`
	}
	var req promoteReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform == "" {
		platform = "web"
	}
	if platform != "web" && platform != "mobile" {
		writeJSONErr(w, http.StatusBadRequest, "invalid_platform", "platform must be 'web' or 'mobile'")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)

	// 1. Read the figma_file row — verifies it exists in this tenant.
	file, err := repo.LookupFigmaFile(r.Context(), fileKey, false)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "file_not_found",
			"file_key is not in this tenant's inventory (may be soft-deleted or never crawled)")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "lookup_file", err.Error())
		return
	}

	// 2. Optional: read the figma_project for a nicer Product label.
	//    Missing project (orphan file) is fine — we fall back to "Figma".
	projectName := "Figma"
	if file.ProjectID != "" {
		if proj, perr := repo.LookupFigmaProject(r.Context(), file.ProjectID); perr == nil {
			projectName = proj.Name
		}
	}

	// 3. Check whether this file is already promoted.
	priorProj, lookupErr := repo.LookupProjectByFileKey(r.Context(), fileKey)
	wasCreated := errors.Is(lookupErr, ErrNotFound)
	if lookupErr != nil && !wasCreated {
		writeJSONErr(w, http.StatusInternalServerError, "lookup_project", lookupErr.Error())
		return
	}

	// 4. Upsert — idempotent on (tenant_id, file_id).
	//    Product = Figma project name, Path = file name. This gives the
	//    existing makeSlug() something stable + human-readable to work
	//    with, and slug collisions get auto-disambiguated by the existing
	//    UpsertProject path (it appends a short hash on UNIQUE conflict).
	ownerID := ""
	if claims != nil {
		ownerID = claims.Sub
	}
	if ownerID == "" {
		// Promote needs a user_id for ownership tracking; fail rather
		// than silently inserting a bare row.
		writeJSONErr(w, http.StatusForbidden, "no_user", "promote requires an authenticated user_id")
		return
	}
	upserted, err := repo.UpsertProject(r.Context(), Project{
		Name:        file.Name,
		Platform:    platform,
		Product:     projectName,
		Path:        file.Name,
		FileID:      fileKey,
		OwnerUserID: ownerID,
	})
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "upsert_project", err.Error())
		return
	}

	// 5. Audit-log the action. Re-promote (created=false) still writes
	//    a row so we can see who re-touched a link and when.
	s.writePromoteAuditLog(r.Context(), tenantID, ownerID, r, promoteAuditDetails{
		FileKey:      fileKey,
		FileName:     file.Name,
		ProjectID:    upserted.ID,
		ProjectSlug:  upserted.Slug,
		Created:      wasCreated,
		PriorProject: priorProj.ID,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":   upserted.ID,
		"project_slug": upserted.Slug,
		"project_name": upserted.Name,
		"created":      wasCreated,
		"file_key":     fileKey,
		"file_name":    file.Name,
	})
}

// ─── audit log writer ───────────────────────────────────────────────────────

type promoteAuditDetails struct {
	FileKey      string `json:"file_key"`
	FileName     string `json:"file_name"`
	ProjectID    string `json:"project_id"`
	ProjectSlug  string `json:"project_slug"`
	Created      bool   `json:"created"`
	PriorProject string `json:"prior_project_id,omitempty"`
}

func (s *Server) writePromoteAuditLog(ctx context.Context, tenantID, userID string, r *http.Request, d promoteAuditDetails) {
	if s.deps.DB == nil {
		return
	}
	details, err := json.Marshal(d)
	if err != nil {
		details = []byte("{}")
	}
	// Mirrors the inline INSERTs at server.go:1547 / 1788 / 3242 — same
	// schema, same time format, same details JSON. We don't go through
	// AuditLogger because that struct only exposes export-shaped writes.
	_, _ = s.deps.DB.DB.ExecContext(ctx, `
		INSERT INTO audit_log (id, ts, event_type, tenant_id, user_id, method, endpoint,
		                      status_code, duration_ms, ip_address, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		uuid.NewString(),
		time.Now().UTC().Format(time.RFC3339Nano),
		"figma_inventory_promote",
		tenantID, userID,
		"POST",
		fmt.Sprintf("/v1/admin/figma-inventory/files/%s/promote", d.FileKey),
		http.StatusOK, 0,
		clientIP(r),
		string(details),
	)
}
