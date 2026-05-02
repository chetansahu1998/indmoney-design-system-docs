package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// Payload caps from the U4 plan section. Hardcoded constants — Phase 1 doesn't
// need tenant-specific overrides.
const (
	MaxBodyBytes      = 10 << 20 // 10 MB
	MaxFlowsPerExport = 20
	// 50 was a Phase 1 placeholder — real Figma sections from the
	// INDstocks file have 70+ frames in a single mode (e.g. "Filters
	// for Stock Screener" light = 70). 200 leaves headroom for a
	// large mobile flow without making the audit pipeline pathological.
	MaxFramesPerFlow  = 200

	MaxStringLen      = 256
	MaxPersonaLen     = 128
)

// allowlistRegex matches the U4-spec regex `[\w \-_/&·]+`. Length is enforced
// separately (regex anchoring would fail on inputs longer than the cap).
//
// Notes on the character set:
//   - \w in Go regexp matches [0-9A-Za-z_]
//   - U+00B7 MIDDLE DOT (·) is included literally so designers can use it as
//     a path separator (e.g. "F&O · Learn").
var allowlistRegex = regexp.MustCompile(`^[\w \-_/&·]+$`)

// validString runs the U4 input-validation rules:
//
//   - non-empty after trimming whitespace,
//   - length <= maxLen (caller passes 256 or 128),
//   - allowlist regex match (no CR / LF / NUL — they aren't in the allowlist),
//   - no embedded NUL bytes (defense-in-depth; the regex would already reject them).
//
// Returns the trimmed input on success, or an error describing what failed.
func validString(field, val string, maxLen int) (string, error) {
	v := strings.TrimSpace(val)
	if v == "" {
		return "", fmt.Errorf("%s: empty", field)
	}
	if len(v) > maxLen {
		return "", fmt.Errorf("%s: exceeds %d chars", field, maxLen)
	}
	for _, r := range v {
		if r == 0 || r == '\r' || r == '\n' {
			return "", fmt.Errorf("%s: contains control character", field)
		}
	}
	if !allowlistRegex.MatchString(v) {
		return "", fmt.Errorf("%s: contains disallowed characters", field)
	}
	return v, nil
}

// ServerDeps wires every external the projects HTTP handlers need. Field-level
// dependency injection keeps tests free to substitute fakes one at a time.
type ServerDeps struct {
	DB             *db.DB
	Broker         sse.BrokerService
	Tickets        sse.TicketStore
	RateLimiter    *RateLimiter
	Idempotency    *IdempotencyCache
	AuditLogger    *AuditLogger
	AuditEnqueuer  *AuditEnqueuer
	DataDir        string

	// PipelineFactory builds a *Pipeline for the given tenant. The factory
	// pattern lets the production wiring inject the per-tenant Figma PAT
	// (decrypted from db.figma_tokens) at request time, and lets tests pass
	// a closure that returns a stubbed renderer.
	PipelineFactory func(ctx context.Context, tenantID string, repo *TenantRepo) (*Pipeline, error)

	// Phase 5.2 P4 — FigmaPATResolver decrypts the tenant's PAT for
	// non-pipeline handlers (figma frame-metadata proxy). nil means the
	// metadata endpoint returns URL-only info without a thumbnail; tests
	// can substitute a closure returning a fake PAT.
	FigmaPATResolver func(ctx context.Context, tenantID string) (string, error)

	Log *slog.Logger
}

// Server bundles handlers + deps. Use NewServer to construct.
type Server struct {
	deps ServerDeps
}

// NewServer returns a configured *Server.
func NewServer(deps ServerDeps) *Server {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Server{deps: deps}
}

// resolveTenantID maps the JWT claims to the single tenant ID this request is
// scoped to. Phase 1 expects exactly one tenant in the token. Returns "" if
// the user belongs to zero or more than one tenant — the handler turns that
// into a 403.
func (s *Server) resolveTenantID(claims *auth.Claims) string {
	if claims == nil {
		return ""
	}
	if len(claims.Tenants) == 1 {
		return claims.Tenants[0]
	}
	// Multi-tenant users must specify via header in Phase 2; reject for now.
	return ""
}

// requireAdminTenant is the shared guard for admin-gated handlers. It
// extracts claims from the request context, checks super-admin, resolves
// the single tenant — and writes the appropriate JSON error envelope on
// failure. Handlers call it at the top:
//
//	tenantID, ok := s.requireAdminTenant(w, r)
//	if !ok { return }
//
// Replaces the inline isAdmin + resolveTenantID pattern that was copied
// across 9 admin handlers in Phase 7+. New admin handlers should adopt
// this helper. Phase 7.7 polish; tracked in
// docs/solutions/2026-05-01-003-phase-7-8-closure.md.
func (s *Server) requireAdminTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if !isAdmin(claims) {
		writeJSONErr(w, http.StatusForbidden, "admin_required", "")
		return "", false
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return "", false
	}
	return tenantID, true
}

// HandleExport serves POST /v1/projects/export.
//
// Lifecycle (per U4):
//
//  1. Decode + validate payload (size cap, flow/frame caps, regex allowlist).
//  2. Resolve tenant_id from JWT claim — request body cannot override.
//  3. Rate-limit (per-user 10/min, per-tenant 200/day).
//  4. Idempotency check — replay within 60s returns 409 + cached body.
//  5. Persist project skeleton in a single transaction.
//  6. audit_log row.
//  7. 202 response + cache it for the idempotency window.
//  8. Spawn pipeline goroutine.
func (s *Server) HandleExport(w http.ResponseWriter, r *http.Request) {
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
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "user has no resolvable tenant for this request")
		return
	}

	// Body size cap — http.MaxBytesReader returns an error we surface as 413.
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	var req ExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// MaxBytesReader exhausting reads as "http: request body too large".
		if strings.Contains(err.Error(), "too large") {
			writeJSONErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("body exceeds %d bytes", MaxBodyBytes))
			return
		}
		writeJSONErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateExport(req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}

	// Rate limit.
	if s.deps.RateLimiter != nil && !s.deps.RateLimiter.Allow(claims.Sub, tenantID) {
		writeJSONErr(w, http.StatusTooManyRequests, "rate_limited",
			"per-user 10/min or per-tenant 200/day cap reached")
		return
	}

	// Idempotency.
	if s.deps.Idempotency != nil {
		if cached, ok := s.deps.Idempotency.Check(req.IdempotencyKey); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write(cached)
			return
		}
	}

	traceID := uuid.NewString()
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)

	// Phase 1 keeps it simple: one flow per export → one project + one version.
	// The plan permits up to 20 flows, so we treat the first flow as the
	// driver and create one version that holds every flow's screens.
	first := req.Flows[0]
	project, err := repo.UpsertProject(r.Context(), Project{
		Name:        first.Name,
		Platform:    first.Platform,
		Product:     first.Product,
		Path:        first.Path,
		OwnerUserID: claims.Sub,
	})
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "upsert_project", err.Error())
		return
	}
	version, err := repo.CreateVersion(r.Context(), project.ID, claims.Sub)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "create_version", err.Error())
		return
	}

	// Iterate flows. Each gets a flow row; each frame becomes a screen row.
	var pipelineFrames []PipelineFrame
	for _, flow := range req.Flows {
		var personaID *string
		if flow.PersonaName != "" {
			persona, wasNew, err := repo.UpsertPersonaTracked(r.Context(), flow.PersonaName, claims.Sub)
			if err != nil {
				writeJSONErr(w, http.StatusInternalServerError, "upsert_persona", err.Error())
				return
			}
			id := persona.ID
			personaID = &id
			// Phase 7.6 — fire a `persona.pending` SSE event when a fresh
			// pending row was created so the admin's bell badge updates
			// without polling. inbox:<tenant_id> is the channel admins
			// already subscribe to (Phase 4 inbox).
			if wasNew && s.deps.Broker != nil {
				s.deps.Broker.Publish(inboxBroadcastChannel(tenantID), sse.PersonaPending{
					Tenant:          tenantID,
					PersonaID:       persona.ID,
					Name:            persona.Name,
					CreatedByUserID: persona.CreatedByUserID,
				})
			}
		}
		f, err := repo.UpsertFlow(r.Context(), Flow{
			ProjectID: project.ID,
			FileID:    req.FileID,
			SectionID: flow.SectionID,
			Name:      flow.Name,
			PersonaID: personaID,
		})
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "upsert_flow", err.Error())
			return
		}

		var screens []Screen
		for _, fr := range flow.Frames {
			screens = append(screens, Screen{
				VersionID: version.ID,
				FlowID:    f.ID,
				X:         fr.X,
				Y:         fr.Y,
				Width:     fr.Width,
				Height:    fr.Height,
			})
		}
		if err := repo.InsertScreens(r.Context(), screens); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "insert_screens", err.Error())
			return
		}
		// Build pipeline frames in the same iteration so we don't lose the
		// (screen ID ↔ Figma frame ID) mapping.
		for i, fr := range flow.Frames {
			pipelineFrames = append(pipelineFrames, PipelineFrame{
				ScreenID:                  screens[i].ID,
				FigmaFrameID:              fr.FrameID,
				X:                         fr.X,
				Y:                         fr.Y,
				Width:                     fr.Width,
				Height:                    fr.Height,
				VariableCollectionID:      fr.VariableCollectionID,
				ModeID:                    fr.ModeID,
				ModeLabel:                 fr.ModeLabel,
				ExplicitVariableModesJSON: fr.ExplicitVariableModesJSON,
			})
		}
	}

	// audit_log row (always — success or failure).
	if s.deps.AuditLogger != nil {
		_ = s.deps.AuditLogger.WriteExport(r.Context(), AuditExportEvent{
			Action:    AuditActionExport,
			UserID:    claims.Sub,
			TenantID:  tenantID,
			FileID:    req.FileID,
			ProjectID: project.ID,
			VersionID: version.ID,
			IP:        clientIP(r),
			UserAgent: r.UserAgent(),
			TraceID:   traceID,
		})
	}

	resp := ExportResponse{
		ProjectID:     project.ID,
		VersionID:     version.ID,
		Deeplink:      "/projects/" + project.Slug + "?v=" + version.ID,
		TraceID:       traceID,
		SchemaVersion: ProjectsSchemaVersion,
	}
	bs, _ := json.Marshal(resp)
	if s.deps.Idempotency != nil && req.IdempotencyKey != "" {
		s.deps.Idempotency.Store(req.IdempotencyKey, bs)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(bs)

	// Spawn pipeline. Use a fresh context — request context is about to be
	// canceled when the handler returns.
	if s.deps.PipelineFactory != nil {
		go func() {
			ctx := context.Background()
			pipeline, err := s.deps.PipelineFactory(ctx, tenantID, repo)
			if err != nil {
				s.deps.Log.Error("pipeline factory", "err", err)
				_ = repo.RecordFailed(ctx, version.ID, "pipeline factory: "+err.Error())
				return
			}
			_ = pipeline.RunFastPreview(ctx, PipelineInputs{
				VersionID:      version.ID,
				ProjectID:      project.ID,
				ProjectSlug:    project.Slug,
				TenantID:       tenantID,
				UserID:         claims.Sub,
				FileID:         req.FileID,
				IdempotencyKey: req.IdempotencyKey,
				TraceID:        traceID,
				IP:             clientIP(r),
				UserAgent:      r.UserAgent(),
				Frames:         pipelineFrames,
			})
		}()
	}
}

// validateExport applies every cap + regex check from the U4 plan section.
// Returns the first error encountered; the handler maps it to 400.
func validateExport(req ExportRequest) error {
	if req.IdempotencyKey == "" {
		return errors.New("idempotency_key: required")
	}
	if len(req.IdempotencyKey) > MaxStringLen {
		return errors.New("idempotency_key: too long")
	}
	if _, err := validString("file_id", req.FileID, MaxStringLen); err != nil {
		return err
	}
	if req.FileName != "" {
		if _, err := validString("file_name", req.FileName, MaxStringLen); err != nil {
			return err
		}
	}
	if len(req.Flows) == 0 {
		return errors.New("flows: required")
	}
	if len(req.Flows) > MaxFlowsPerExport {
		return fmt.Errorf("flows: max %d per request", MaxFlowsPerExport)
	}
	for i, flow := range req.Flows {
		if len(flow.Frames) == 0 {
			return fmt.Errorf("flows[%d].frames: required", i)
		}
		if len(flow.Frames) > MaxFramesPerFlow {
			return fmt.Errorf("flows[%d].frames: max %d per flow", i, MaxFramesPerFlow)
		}
		if _, err := validString(fmt.Sprintf("flows[%d].name", i), flow.Name, MaxStringLen); err != nil {
			return err
		}
		if _, err := validString(fmt.Sprintf("flows[%d].platform", i), flow.Platform, MaxStringLen); err != nil {
			return err
		}
		if _, err := validString(fmt.Sprintf("flows[%d].product", i), flow.Product, MaxStringLen); err != nil {
			return err
		}
		if _, err := validString(fmt.Sprintf("flows[%d].path", i), flow.Path, MaxStringLen); err != nil {
			return err
		}
		if flow.PersonaName != "" {
			if _, err := validString(fmt.Sprintf("flows[%d].persona_name", i), flow.PersonaName, MaxPersonaLen); err != nil {
				return err
			}
		}
		for j, fr := range flow.Frames {
			if fr.FrameID == "" {
				return fmt.Errorf("flows[%d].frames[%d].frame_id: required", i, j)
			}
			if len(fr.FrameID) > MaxStringLen {
				return fmt.Errorf("flows[%d].frames[%d].frame_id: too long", i, j)
			}
		}
	}
	return nil
}

// HandleProjectGet serves GET /v1/projects/{slug}.
func (s *Server) HandleProjectGet(w http.ResponseWriter, r *http.Request) {
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
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	p, err := repo.GetProjectBySlug(r.Context(), slug)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"project": p})
		return
	}
	if !errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	// Phase 7.5 — flow_aliases redirect. The slug doesn't match an active
	// project; check whether it's the prior slug of a renamed flow. If
	// found, 301 to the live slug. Browsers + the frontend's fetch path
	// follow the redirect transparently; pasted URLs from before the
	// rename keep working.
	if redirectedTo, lookErr := repo.LookupFlowAlias(r.Context(), slug); lookErr == nil && redirectedTo != "" {
		// Build the live URL preserving the original path tail (anything
		// past /v1/projects/{slug}). This keeps query strings + sub-paths
		// like /v1/projects/{slug}/screens/{id} working through the
		// redirect.
		newPath := strings.Replace(r.URL.Path, "/"+slug, "/"+redirectedTo, 1)
		if r.URL.RawQuery != "" {
			newPath += "?" + r.URL.RawQuery
		}
		w.Header().Set("Location", newPath)
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	writeJSONErr(w, http.StatusNotFound, "not_found", "")
}

// HandleListViolations serves GET /v1/projects/{slug}/violations.
//
// Returns the violations for the given project, scoped to a specific
// version (?v=<id>, or latest if omitted). Backs the Violations tab on
// the /projects/<slug> page. Filters mirror lib/projects/client.ts
// listViolations() — persona_id, mode_label, category (comma-joined).
//
// Was missing for Phase 7.8 — the frontend client.ts shipped before
// the handler did, so every project page logged a 404 on the
// Violations tab while the rest of the page rendered fine.
func (s *Server) HandleListViolations(w http.ResponseWriter, r *http.Request) {
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
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	project, err := repo.GetProjectBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	// Resolve version: explicit ?v= or latest by version_index for the project.
	versionID := r.URL.Query().Get("v")
	if versionID == "" {
		err := s.deps.DB.DB.QueryRowContext(r.Context(),
			`SELECT id FROM project_versions
			 WHERE project_id = ? AND tenant_id = ?
			 ORDER BY version_index DESC LIMIT 1`,
			project.ID, tenantID,
		).Scan(&versionID)
		if errors.Is(err, sql.ErrNoRows) {
			// No versions yet — return empty result, not 404. Frontend
			// renders an empty-state pane.
			writeJSON(w, http.StatusOK, map[string]any{"violations": []any{}, "count": 0})
			return
		}
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "version_lookup", err.Error())
			return
		}
	}

	// Build the WHERE clause incrementally so optional filters compose without
	// SQL injection risk — only fixed property names go into the SQL string;
	// values are always parameterized.
	args := []any{versionID, tenantID}
	where := "version_id = ? AND tenant_id = ?"
	if v := r.URL.Query().Get("persona_id"); v != "" {
		where += " AND persona_id = ?"
		args = append(args, v)
	}
	if v := r.URL.Query().Get("mode_label"); v != "" {
		where += " AND mode_label = ?"
		args = append(args, v)
	}
	if v := r.URL.Query().Get("category"); v != "" {
		// Comma-joined multi-select — matches the frontend's serialization.
		parts := strings.Split(v, ",")
		ph := strings.Repeat("?,", len(parts))
		ph = strings.TrimRight(ph, ",")
		where += " AND category IN (" + ph + ")"
		for _, p := range parts {
			args = append(args, strings.TrimSpace(p))
		}
	}

	// active first → acknowledged → dismissed → fixed; within a status tier
	// the inbox-style sort by severity then created_at gives the operator a
	// stable read order across re-fetches.
	q := `SELECT id, version_id, screen_id, tenant_id, rule_id, severity,
	             COALESCE(category, 'token_drift'),
	             property, observed, suggestion, persona_id, mode_label,
	             status, COALESCE(auto_fixable, 0), created_at
	      FROM violations
	      WHERE ` + where + `
	      ORDER BY
	        CASE status WHEN 'active' THEN 0 WHEN 'acknowledged' THEN 1
	                    WHEN 'dismissed' THEN 2 WHEN 'fixed' THEN 3 ELSE 4 END,
	        CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1
	                      WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,
	        created_at DESC`
	rows, err := s.deps.DB.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "query", err.Error())
		return
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var v Violation
		var personaID, modeLabel sql.NullString
		var observed, suggestion sql.NullString
		var createdAt string
		var autoFixInt int
		if err := rows.Scan(&v.ID, &v.VersionID, &v.ScreenID, &v.TenantID,
			&v.RuleID, &v.Severity, &v.Category,
			&v.Property, &observed, &suggestion,
			&personaID, &modeLabel,
			&v.Status, &autoFixInt, &createdAt); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "scan", err.Error())
			return
		}
		row := map[string]any{
			"id":           v.ID,
			"version_id":   v.VersionID,
			"screen_id":    v.ScreenID,
			"rule_id":      v.RuleID,
			"severity":     v.Severity,
			"category":     v.Category,
			"property":     v.Property,
			"observed":     observed.String,
			"suggestion":   suggestion.String,
			"status":       v.Status,
			"auto_fixable": autoFixInt == 1,
			"created_at":   createdAt,
		}
		if personaID.Valid {
			row["persona_id"] = personaID.String
		}
		if modeLabel.Valid {
			row["mode_label"] = modeLabel.String
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "rows", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"violations": out,
		"count":      len(out),
		"version_id": versionID,
	})
}

// HandleProjectList serves GET /v1/projects.
func (s *Server) HandleProjectList(w http.ResponseWriter, r *http.Request) {
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
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	list, err := repo.ListProjects(r.Context(), 100)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projects": list,
		"count":    len(list),
	})
}

// HandleEventsTicket serves POST /v1/projects/{slug}/events/ticket.
//
// MaxDRDBodyBytes caps PUT body size for DRD content (per Phase 1 plan H4).
// 1MB is enough for a 50-paragraph document with light embeds; oversize
// uploads should go through asset CDN in Phase 5.
const MaxDRDBodyBytes = 1 << 20

// HandleGetDRD serves GET /v1/projects/:slug/flows/:flow_id/drd.
// First-fetch returns `{revision:0, content:"{}"}` when no row exists yet —
// the editor starts blank without a separate "create" step.
func (s *Server) HandleGetDRD(w http.ResponseWriter, r *http.Request) {
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
	flowID := r.PathValue("flow_id")
	if slug == "" || flowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	rec, err := repo.GetDRD(r.Context(), slug, flowID)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "drd_lookup", err.Error())
		return
	}

	// Embed content_json verbatim so the BlockNote document doesn't get
	// double-escaped through json.Marshal of a []byte.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.WriteHeader(http.StatusOK)
	flowJSON, _ := json.Marshal(rec.FlowID)
	w.Write([]byte(`{"flow_id":`))
	w.Write(flowJSON)
	w.Write([]byte(`,"content":`))
	if len(rec.ContentJSON) == 0 {
		w.Write([]byte("{}"))
	} else {
		w.Write(rec.ContentJSON)
	}
	w.Write([]byte(fmt.Sprintf(`,"revision":%d`, rec.Revision)))
	w.Write([]byte(`,"updated_at":`))
	if rec.UpdatedAt.IsZero() {
		w.Write([]byte("null"))
	} else {
		ts, _ := json.Marshal(rec.UpdatedAt.UTC().Format(time.RFC3339))
		w.Write(ts)
	}
	w.Write([]byte(`,"updated_by":`))
	if rec.UpdatedByUser == "" {
		w.Write([]byte("null"))
	} else {
		ub, _ := json.Marshal(rec.UpdatedByUser)
		w.Write(ub)
	}
	w.Write([]byte("}"))
}

// HandlePutDRD serves PUT /v1/projects/:slug/flows/:flow_id/drd.
// Body shape: { "content": <BlockNote JSON>, "expected_revision": <int> }.
// Conflict (409): { "error": "revision_conflict", "current_revision": <int> }.
// Success (200): { "revision": <new int>, "updated_at": "..." }.
// Body capped at MaxDRDBodyBytes (1MB); oversize → 413.
func (s *Server) HandlePutDRD(w http.ResponseWriter, r *http.Request) {
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
	flowID := r.PathValue("flow_id")
	if slug == "" || flowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxDRDBodyBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			writeJSONErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("DRD body exceeds %d bytes; oversize images belong in asset CDN (Phase 5)", MaxDRDBodyBytes))
			return
		}
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}

	var req struct {
		Content          json.RawMessage `json:"content"`
		ExpectedRevision int             `json:"expected_revision"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(req.Content) == 0 {
		req.Content = []byte("{}")
	}
	if req.ExpectedRevision < 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid_revision", "expected_revision must be >= 0")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	newRev, err := repo.UpsertDRD(r.Context(), slug, flowID, []byte(req.Content), req.ExpectedRevision, claims.Sub)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if errors.Is(err, ErrRevisionConflict) {
		rec, _ := repo.GetDRD(r.Context(), slug, flowID)
		current := 0
		if rec != nil {
			current = rec.Revision
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":            "revision_conflict",
			"current_revision": current,
		})
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "drd_upsert", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revision":   newRev,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleScreenCanonicalTree serves the lazy canonical_tree JSON for a single
// screen. Called by the U8 JSON tab on click. Tenant-scoped via TenantRepo;
// cross-tenant returns 404.
//
// The canonical_tree is stored as raw JSON text (`screen_canonical_trees.canonical_tree`),
// and we pass it through unparsed — the client treats it as `unknown` and the
// tree-viewer in U8 walks it generically.
func (s *Server) HandleScreenCanonicalTree(w http.ResponseWriter, r *http.Request) {
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
	if slug == "" || screenID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	res, err := repo.GetCanonicalTree(r.Context(), slug, screenID)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "canonical_tree_lookup", err.Error())
		return
	}

	// Pass canonical_tree through verbatim — it's stored as JSON text, so we
	// embed it directly rather than re-parsing/re-serializing.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.WriteHeader(http.StatusOK)
	// Hand-crafted response so the canonical_tree blob isn't double-escaped.
	w.Write([]byte(`{"canonical_tree":`))
	w.Write([]byte(res.Tree))
	w.Write([]byte(`,"hash":`))
	if res.Hash == "" {
		w.Write([]byte("null"))
	} else {
		hashJSON, _ := json.Marshal(res.Hash)
		w.Write(hashJSON)
	}
	w.Write([]byte("}"))
}

// JWT-authed. Issues a single-use ticket bound to (user, tenant, trace). The
// trace_id is supplied by the client via the X-Trace-ID header — typically the
// trace_id from the export response.
func (s *Server) HandleEventsTicket(w http.ResponseWriter, r *http.Request) {
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
	traceID := r.Header.Get("X-Trace-ID")
	if traceID == "" {
		// Trace ID may also be provided via body; either is fine.
		var body struct {
			TraceID string `json:"trace_id"`
		}
		bs, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		_ = json.Unmarshal(bs, &body)
		traceID = body.TraceID
	}
	if traceID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_trace", "X-Trace-ID header or body.trace_id required")
		return
	}

	// Defense in depth: confirm the slug really belongs to this tenant.
	// Cross-tenant slug → 404 (no existence oracle).
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.GetProjectBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	if s.deps.Tickets == nil {
		writeJSONErr(w, http.StatusInternalServerError, "no_ticket_store", "")
		return
	}
	ticket := s.deps.Tickets.IssueTicket(claims.Sub, tenantID, traceID, sse.DefaultTicketTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"trace_id":   traceID,
		"expires_in": int(sse.DefaultTicketTTL.Seconds()),
	})
}

// HandleProjectEvents serves GET /v1/projects/{slug}/events?ticket=...
//
// NOT JWT-authed (Authorization header in EventSource is impossible across
// browsers). Auth is the single-use ticket: redeem it, check tenant, subscribe.
func (s *Server) HandleProjectEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tickets == nil || s.deps.Broker == nil {
		writeJSONErr(w, http.StatusInternalServerError, "sse_not_configured", "")
		return
	}
	// Reject JWT-in-query-string defensively.
	if r.URL.Query().Get("token") != "" || r.URL.Query().Get("authorization") != "" {
		s.deps.Log.Warn("sse: client passed JWT in query string", "remote", r.RemoteAddr)
		writeJSONErr(w, http.StatusBadRequest, "no_jwt_in_query", "use ?ticket=... not ?token=...")
		return
	}
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		writeJSONErr(w, http.StatusUnauthorized, "missing_ticket", "")
		return
	}
	userID, tenantID, traceID, ok := s.deps.Tickets.RedeemTicket(ticket)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "invalid_ticket", "")
		return
	}

	// Defense in depth: project's tenant_id must match ticket's tenant_id.
	slug := r.PathValue("slug")
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.GetProjectBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusForbidden, "tenant_mismatch", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError, "no_streaming", "")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub, err := s.deps.Broker.Subscribe(traceID, tenantID, userID)
	if err != nil {
		if errors.Is(err, sse.ErrSubscriberCapReached) {
			writeJSONErr(w, http.StatusServiceUnavailable, "subscribers_full", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "subscribe", err.Error())
		return
	}
	defer unsub()

	clientGone := r.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case ev, alive := <-ch:
			if !alive {
				return
			}
			if sse.IsHeartbeat(ev) {
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
				continue
			}
			payload, _ := json.Marshal(ev.Payload())
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type(), payload)
			flusher.Flush()
		}
	}
}

// ─── Phase 4: violation lifecycle ────────────────────────────────────────────

// patchViolationRequest is the JSON body for PATCH
// /v1/projects/{slug}/violations/{id}.
//
// Action is required ("acknowledge" | "dismiss" | "reactivate"); the
// system-only "mark_fixed" action is not exposed on this endpoint — the
// plugin's POST /v1/projects/{slug}/violations/{id}/fix-applied (Phase 4 U12)
// is the only legal entry point for that transition.
type patchViolationRequest struct {
	Action          string `json:"action"`
	Reason          string `json:"reason,omitempty"`
	// Phase 5 U11 — when present, the lifecycle write also inserts a
	// decision_links row with link_type='violation' inside the same
	// transaction. Empty string ⇒ no link.
	LinkDecisionID  string `json:"link_decision_id,omitempty"`
}

// HandlePatchViolation serves PATCH /v1/projects/{slug}/violations/{id}.
//
// Lifecycle (per Phase 4 U1):
//   1. Auth + tenant resolution.
//   2. Decode + validate body (action enum, reason length).
//   3. Load current violation (cross-tenant 404).
//   4. Validate transition against actor role.
//   5. UPDATE violations + audit_log row in a single transaction.
//   6. SSE publish on the version's trace_id (best-effort; failure logged).
//   7. 200 with the new status.
func (s *Server) HandlePatchViolation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "PATCH only")
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
	violationID := r.PathValue("id")
	if slug == "" || violationID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req patchViolationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	action, err := ParseLifecycleAction(req.Action)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_action", err.Error())
		return
	}
	// mark_fixed is reserved for the plugin auto-fix endpoint.
	if action == ActionMarkFixed {
		writeJSONErr(w, http.StatusForbidden, "system_only_action", "use /violations/{id}/fix-applied")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	current, err := repo.GetViolationForLifecycle(r.Context(), violationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	// Defense in depth: caller-supplied slug must match the project the
	// violation actually belongs to. Prevents an admin in tenant A from acting
	// on tenant A's violation through tenant B's project URL.
	if current.ProjectSlug != slug {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}

	transition, err := ValidateTransition(current.From, action, claims.Role, req.Reason, false)
	if err != nil {
		switch {
		case errors.Is(err, ErrForbiddenRole):
			writeJSONErr(w, http.StatusForbidden, "forbidden", err.Error())
		case errors.Is(err, ErrReasonRequired), errors.Is(err, ErrReasonTooLong),
			errors.Is(err, ErrInvalidAction), errors.Is(err, ErrInvalidTransition):
			writeJSONErr(w, http.StatusBadRequest, "invalid_transition", err.Error())
		default:
			writeJSONErr(w, http.StatusBadRequest, "invalid_transition", err.Error())
		}
		return
	}

	// Build the audit_log writer that runs inside the same transaction. The
	// details JSON intentionally mirrors the Phase 0 audit shape so audit_log
	// queries don't need to special-case violation rows.
	now := time.Now().UTC()
	traceID := current.TraceID
	auditDetails, _ := json.Marshal(map[string]any{
		"violation_id": current.ViolationID,
		"version_id":   current.VersionID,
		"project_slug": current.ProjectSlug,
		"from":         transition.From,
		"to":           transition.To,
		"reason":       transition.Reason,
		"trace_id":     traceID,
		"schema_ver":   ProjectsSchemaVersion,
	})
	// Phase 4 U3 — resolve carry-forward identity (screen_logical_id +
	// rule_id + property) outside the lifecycle tx. The tuple is effectively
	// immutable for a given violation row, so reading it before the UPDATE
	// is safe and keeps the lifecycle tx single-statement-light.
	var cfLogical, cfRule, cfProp string
	if transition.Action == ActionDismiss || transition.Action == ActionReactivate {
		l, ru, pr, kerr := ResolveCarryForwardKey(r.Context(), s.deps.DB.DB, tenantID, violationID)
		if kerr != nil && !errors.Is(kerr, ErrNotFound) {
			writeJSONErr(w, http.StatusInternalServerError, "resolve_carry_forward", kerr.Error())
			return
		}
		cfLogical, cfRule, cfProp = l, ru, pr
	}

	auditWrite := func(tx *sql.Tx) error {
		if _, ierr := tx.ExecContext(r.Context(),
			`INSERT INTO audit_log (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(),
			now.Format(time.RFC3339Nano),
			transition.AuditEventType(),
			tenantID,
			claims.Sub,
			"PATCH",
			fmt.Sprintf("/v1/projects/%s/violations/%s", slug, violationID),
			http.StatusOK,
			0,
			clientIP(r),
			string(auditDetails),
		); ierr != nil {
			return ierr
		}

		// Phase 5 U11 — link to a decision when the caller provided one.
		// Validate the decision exists in this tenant before linking;
		// silently drop unknown ids (defense in depth — caller might be
		// passing a stale id from an old decisions list).
		if req.LinkDecisionID != "" {
			var exists string
			lookupErr := tx.QueryRowContext(r.Context(),
				`SELECT id FROM decisions WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
				req.LinkDecisionID, tenantID,
			).Scan(&exists)
			if lookupErr == nil {
				if _, ierr := tx.ExecContext(r.Context(),
					`INSERT OR IGNORE INTO decision_links
					 (decision_id, link_type, target_id, tenant_id, created_at)
					 VALUES (?, ?, ?, ?, ?)`,
					req.LinkDecisionID, string(LinkTypeViolation), violationID, tenantID,
					now.Format(time.RFC3339),
				); ierr != nil {
					return fmt.Errorf("link decision: %w", ierr)
				}
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return fmt.Errorf("decision lookup: %w", lookupErr)
			}
		}

		// Carry-forward marker bookkeeping. Runs in the same transaction as
		// the status flip + audit_log row so all three are atomic.
		if cfLogical == "" {
			return nil
		}
		switch transition.Action {
		case ActionDismiss:
			return WriteCarryForwardMarker(r.Context(), tx, CarryForwardMarker{
				TenantID:            tenantID,
				ScreenLogicalID:     cfLogical,
				RuleID:              cfRule,
				Property:            cfProp,
				Reason:              transition.Reason,
				DismissedByUserID:   claims.Sub,
				DismissedAt:         now,
				OriginalViolationID: current.ViolationID,
			})
		case ActionReactivate:
			// Only delete the marker when the prior state was Dismissed; an
			// admin reactivating Acknowledged shouldn't touch carry-forwards.
			if transition.From != ViolationStatusDismissed {
				return nil
			}
			return DeleteCarryForwardMarker(r.Context(), tx, tenantID, cfLogical, cfRule, cfProp)
		}
		return nil
	}

	if err := repo.UpdateViolationStatus(r.Context(), violationID, transition, auditWrite); err != nil {
		if errors.Is(err, ErrNotFound) {
			// Race window: the row was modified between GET and UPDATE.
			// Surface as 409 so the client can refetch and retry.
			writeJSONErr(w, http.StatusConflict, "race", "violation status changed concurrently")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "update_violation", err.Error())
		return
	}

	// Best-effort SSE fan-out. Subscribers (Violations tab + Inbox) reconcile
	// row state; a missed publish only means the client's next poll/refetch
	// will catch up — never blocks the lifecycle write.
	if s.deps.Broker != nil {
		ev := sse.ProjectViolationLifecycleChanged{
			ProjectSlug: current.ProjectSlug,
			VersionID:   current.VersionID,
			ViolationID: current.ViolationID,
			Tenant:      tenantID,
			From:        transition.From,
			To:          transition.To,
			Action:      string(transition.Action),
			ActorUserID: claims.Sub,
		}
		if traceID != "" {
			s.deps.Broker.Publish(traceID, ev)
		}
		// Phase 4.1 — also broadcast to the tenant inbox channel so
		// /inbox subscribers reconcile across projects.
		s.deps.Broker.Publish(inboxBroadcastChannel(tenantID), ev)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"violation_id": current.ViolationID,
		"from":         transition.From,
		"to":           transition.To,
		"action":       string(transition.Action),
	})
}

// MaxBulkLifecycleRows caps how many violation IDs a single bulk request may
// touch. Matches the Phase 4 plan ("Cap at 100 rows per request") — protects
// the audit_log against runaway batches.
const MaxBulkLifecycleRows = 100

// bulkLifecycleRequest is the JSON body for POST
// /v1/projects/{slug}/violations/bulk-acknowledge.
type bulkLifecycleRequest struct {
	Action       string   `json:"action"` // acknowledge | dismiss | reactivate
	Reason       string   `json:"reason,omitempty"`
	ViolationIDs []string `json:"violation_ids"`
}

// HandleBulkAcknowledge serves POST
// /v1/projects/{slug}/violations/bulk-acknowledge.
//
// All ids must belong to the slug-scoped project + the caller's tenant. Up to
// MaxBulkLifecycleRows ids per request. Per-row audit_log entries share a
// `bulk_id` so post-hoc analytics can re-aggregate them. SSE fan-out emits
// one ProjectViolationLifecycleChanged per updated row, throttled by the
// broker's per-channel buffer.
func (s *Server) HandleBulkAcknowledge(w http.ResponseWriter, r *http.Request) {
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req bulkLifecycleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	if len(req.ViolationIDs) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "empty_ids", "violation_ids required")
		return
	}
	if len(req.ViolationIDs) > MaxBulkLifecycleRows {
		writeJSONErr(w, http.StatusBadRequest, "too_many_ids", fmt.Sprintf("max %d ids", MaxBulkLifecycleRows))
		return
	}
	action, err := ParseLifecycleAction(req.Action)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_action", err.Error())
		return
	}
	if action == ActionMarkFixed {
		writeJSONErr(w, http.StatusForbidden, "system_only_action", "use /violations/{id}/fix-applied")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	loaded, err := repo.LoadViolationsForBulk(r.Context(), req.ViolationIDs)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	// Build the bulk row set. Anything missing from the DB or owned by another
	// project slug becomes a skipped id; anything that fails ValidateTransition
	// also lands in skipped (over-cautious for a bulk endpoint, but keeps the
	// per-id contract uniform with U1).
	bulkID := uuid.NewString()
	now := time.Now().UTC()
	loadedByID := make(map[string]ViolationLifecycleResult, len(loaded))
	for _, v := range loaded {
		loadedByID[v.ViolationID] = v
	}

	rows := make([]BulkLifecycleRow, 0, len(req.ViolationIDs))
	skipped := make([]string, 0)
	publishCandidates := make([]ViolationLifecycleResult, 0, len(loaded))

	for _, id := range req.ViolationIDs {
		v, ok := loadedByID[id]
		if !ok || v.ProjectSlug != slug {
			skipped = append(skipped, id)
			continue
		}
		transition, terr := ValidateTransition(v.From, action, claims.Role, req.Reason, false)
		if terr != nil {
			skipped = append(skipped, id)
			continue
		}
		// Capture in a closure so each row gets its own audit_log payload.
		t := transition
		row := v
		rows = append(rows, BulkLifecycleRow{
			ViolationID: row.ViolationID,
			From:        t.From,
			To:          t.To,
			PerRowAudit: func(tx *sql.Tx, vID, from, to string) error {
				details, _ := json.Marshal(map[string]any{
					"violation_id": vID,
					"version_id":   row.VersionID,
					"project_slug": row.ProjectSlug,
					"from":         from,
					"to":           to,
					"reason":       t.Reason,
					"trace_id":     row.TraceID,
					"bulk_id":      bulkID,
					"schema_ver":   ProjectsSchemaVersion,
				})
				_, ierr := tx.ExecContext(r.Context(),
					`INSERT INTO audit_log (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
					 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					uuid.NewString(),
					now.Format(time.RFC3339Nano),
					t.AuditEventType(),
					tenantID,
					claims.Sub,
					"POST",
					fmt.Sprintf("/v1/projects/%s/violations/bulk-acknowledge", slug),
					http.StatusOK,
					0,
					clientIP(r),
					string(details),
				)
				return ierr
			},
		})
		publishCandidates = append(publishCandidates, row)
	}

	summary, err := repo.BulkUpdateViolationStatus(r.Context(), rows)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "bulk_update", err.Error())
		return
	}

	// Merge "validation skip" + "DB-side skip" sets so the API consumer sees
	// both classes uniformly under `skipped`.
	if len(summary.Skipped) > 0 {
		skipped = append(skipped, summary.Skipped...)
	}

	// Best-effort SSE fan-out for actually-updated rows.
	if s.deps.Broker != nil {
		updatedSet := make(map[string]struct{}, len(summary.Updated))
		for _, id := range summary.Updated {
			updatedSet[id] = struct{}{}
		}
		inboxChannel := inboxBroadcastChannel(tenantID)
		for _, v := range publishCandidates {
			if _, ok := updatedSet[v.ViolationID]; !ok {
				continue
			}
			ev := sse.ProjectViolationLifecycleChanged{
				ProjectSlug: v.ProjectSlug,
				VersionID:   v.VersionID,
				ViolationID: v.ViolationID,
				Tenant:      tenantID,
				From:        "active",
				To:          targetStatusFor(action),
				Action:      string(action),
				ActorUserID: claims.Sub,
			}
			if v.TraceID != "" {
				s.deps.Broker.Publish(v.TraceID, ev)
			}
			s.deps.Broker.Publish(inboxChannel, ev)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bulk_id": bulkID,
		"updated": summary.Updated,
		"skipped": skipped,
		"action":  string(action),
	})
}

// ─── Phase 5 U3: Decisions ───────────────────────────────────────────────────

// createDecisionRequest mirrors the JSON body for
// POST /v1/projects/{slug}/flows/{flow_id}/decisions.
type createDecisionRequest struct {
	Title         string                  `json:"title"`
	BodyJSON      json.RawMessage         `json:"body_json,omitempty"`
	Status        string                  `json:"status,omitempty"`
	SupersedesID  string                  `json:"supersedes_id,omitempty"`
	Links         []decisionLinkRequest   `json:"links,omitempty"`
	VersionID     string                  `json:"version_id,omitempty"`
}

type decisionLinkRequest struct {
	LinkType string `json:"link_type"`
	TargetID string `json:"target_id"`
}

// HandleDecisionCreate serves POST /v1/projects/{slug}/flows/{flow_id}/decisions.
//
// Requires the caller to be authenticated + tenant-scoped. The flow must be
// visible inside the caller's tenant; cross-tenant attempts return 404.
// Validation errors map to 400; cycle errors to 409; not-found to 404.
func (s *Server) HandleDecisionCreate(w http.ResponseWriter, r *http.Request) {
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
	flowID := r.PathValue("flow_id")
	if slug == "" || flowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req createDecisionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}

	links := make([]DecisionLinkInput, 0, len(req.Links))
	for _, l := range req.Links {
		links = append(links, DecisionLinkInput{
			LinkType: LinkType(l.LinkType),
			TargetID: l.TargetID,
		})
	}
	in, err := ValidateDecisionInput(DecisionInput{
		Title:        req.Title,
		BodyJSON:     []byte(req.BodyJSON),
		Status:       req.Status,
		SupersedesID: req.SupersedesID,
		Links:        links,
	})
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_decision", err.Error())
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)

	// Defense in depth: confirm the slug refers to a project that owns
	// this flow inside the caller's tenant. The repo's
	// assertFlowVisibleByID already checks tenant; we additionally
	// verify the slug match here so a tenant-A admin can't act on a
	// tenant-A flow through a tenant-B slug URL.
	if _, err := repo.GetProjectBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	rec, err := repo.CreateDecision(r.Context(), flowID, req.VersionID, claims.Sub, in)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeJSONErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, ErrDecisionCycle):
			writeJSONErr(w, http.StatusConflict, "decision_cycle", err.Error())
		default:
			writeJSONErr(w, http.StatusInternalServerError, "create_decision", err.Error())
		}
		return
	}

	// Audit-log row in best-effort fashion. Decisions are created in
	// their own transaction (the cycle check + UPDATE chain is too much
	// to run inside the audit_log writer hook); we log post-write here.
	now := time.Now().UTC()
	details, _ := json.Marshal(map[string]any{
		"decision_id":   rec.ID,
		"flow_id":       rec.FlowID,
		"version_id":    rec.VersionID,
		"status":        rec.Status,
		"supersedes_id": rec.SupersedesID,
		"project_slug":  slug,
		"schema_ver":    ProjectsSchemaVersion,
	})
	_ = s.deps.DB.WriteAudit(r.Context(), db.AuditEntry{
		ID:         uuid.NewString(),
		TS:         now,
		EventType:  MakeDecisionAuditEvent("create"),
		TenantID:   tenantID,
		UserID:     claims.Sub,
		Method:     "POST",
		Endpoint:   r.URL.Path,
		StatusCode: http.StatusCreated,
		IPAddress:  clientIP(r),
		Details:    string(details),
	})

	// Phase 5.2 P3 — broadcast on the tenant inbox channel so any
	// embedded decisionRef blocks in the DRD re-fetch + render the new
	// state. The action verb lets clients pick the right animation.
	if s.deps.Broker != nil {
		s.deps.Broker.Publish(inboxBroadcastChannel(tenantID), sse.ProjectDecisionChanged{
			ProjectSlug: slug,
			FlowID:      rec.FlowID,
			DecisionID:  rec.ID,
			Tenant:      tenantID,
			Status:      rec.Status,
			Action:      "created",
			ActorUserID: claims.Sub,
		})
		// If this decision supersedes another, the predecessor's status
		// also flipped — broadcast a second event so any embedded ref
		// to the predecessor re-renders as superseded.
		if rec.SupersedesID != nil && *rec.SupersedesID != "" {
			s.deps.Broker.Publish(inboxBroadcastChannel(tenantID), sse.ProjectDecisionChanged{
				ProjectSlug: slug,
				FlowID:      rec.FlowID,
				DecisionID:  *rec.SupersedesID,
				Tenant:      tenantID,
				Status:      "superseded",
				Action:      "superseded",
				ActorUserID: claims.Sub,
			})
		}
	}

	writeJSON(w, http.StatusCreated, rec)
}

// HandleDecisionList serves GET /v1/projects/{slug}/flows/{flow_id}/decisions
// with optional ?include_superseded=1 toggle.
func (s *Server) HandleDecisionList(w http.ResponseWriter, r *http.Request) {
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
	flowID := r.PathValue("flow_id")
	if flowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_flow_id", "")
		return
	}
	includeSuperseded := r.URL.Query().Get("include_superseded") == "1"

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	out, err := repo.ListDecisionsForFlow(r.Context(), flowID, includeSuperseded)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "list_decisions", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"decisions": out,
		"count":     len(out),
	})
}

// HandleDecisionGet serves GET /v1/decisions/{id}. Tenant-scoped.
func (s *Server) HandleDecisionGet(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if id == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_id", "")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	rec, err := repo.GetDecision(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "get_decision", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// HandleDecisionViolations serves GET /v1/decisions/{id}/violations.
//
// Phase 6 U7 — backs the "Linked violations" subsection on DecisionCard.
// Joins decision_links (link_type='violation') with violations on
// target_id = violation.id, scoped to the same tenant. Returns the same
// row shape as HandleListViolations so the frontend Violation type stays
// shared.
//
// Empty result is a 200 with {violations: [], count: 0} — the card
// renders a "No linked violations" empty state.
func (s *Server) HandleDecisionViolations(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if id == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_id", "")
		return
	}

	// Verify the decision belongs to this tenant before returning links.
	// Same gate as HandleDecisionGet so a leaked decision id doesn't enable
	// cross-tenant violation enumeration.
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.GetDecision(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup_decision", err.Error())
		return
	}

	q := `SELECT v.id, v.version_id, v.screen_id, v.tenant_id, v.rule_id, v.severity,
	             COALESCE(v.category, 'token_drift'),
	             v.property, v.observed, v.suggestion, v.persona_id, v.mode_label,
	             v.status, COALESCE(v.auto_fixable, 0), v.created_at
	      FROM decision_links dl
	      JOIN violations v ON v.id = dl.target_id AND v.tenant_id = dl.tenant_id
	      WHERE dl.decision_id = ? AND dl.tenant_id = ? AND dl.link_type = 'violation'
	      ORDER BY
	        CASE v.status WHEN 'active' THEN 0 WHEN 'acknowledged' THEN 1
	                      WHEN 'dismissed' THEN 2 WHEN 'fixed' THEN 3 ELSE 4 END,
	        CASE v.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1
	                        WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,
	        v.created_at DESC`
	rows, err := s.deps.DB.DB.QueryContext(r.Context(), q, id, tenantID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "query", err.Error())
		return
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var v Violation
		var personaID, modeLabel sql.NullString
		var observed, suggestion sql.NullString
		var createdAt string
		var autoFixInt int
		if err := rows.Scan(&v.ID, &v.VersionID, &v.ScreenID, &v.TenantID,
			&v.RuleID, &v.Severity, &v.Category,
			&v.Property, &observed, &suggestion,
			&personaID, &modeLabel,
			&v.Status, &autoFixInt, &createdAt); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "scan", err.Error())
			return
		}
		row := map[string]any{
			"id":           v.ID,
			"version_id":   v.VersionID,
			"screen_id":    v.ScreenID,
			"tenant_id":    v.TenantID,
			"rule_id":      v.RuleID,
			"severity":     v.Severity,
			"category":     v.Category,
			"property":     v.Property,
			"observed":     observed.String,
			"suggestion":   suggestion.String,
			"status":       v.Status,
			"auto_fixable": autoFixInt == 1,
			"created_at":   createdAt,
		}
		if personaID.Valid {
			row["persona_id"] = personaID.String
		}
		if modeLabel.Valid {
			row["mode_label"] = modeLabel.String
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "rows", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"violations": out,
		"count":      len(out),
	})
}

// HandleAdminReactivateDecision serves POST /v1/atlas/admin/decisions/{id}/reactivate.
// Super-admin only — main.go gates with requireSuperAdmin.
//
// Flips a superseded decision back to 'accepted' + clears its
// superseded_by_id. Idempotent on a non-superseded row (returns 200 +
// {updated: 0}); the caller can re-trigger without breaking the chain.
//
// Audit-log row writes after the UPDATE so the activity rail surfaces
// the moderation action under decision.admin_reactivate.
func (s *Server) HandleAdminReactivateDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_id", "")
		return
	}
	repoDB := NewDB(s.deps.DB.DB)
	outcome, err := repoDB.AdminReactivateDecision(r.Context(), id)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "reactivate", err.Error())
		return
	}
	if outcome.Updated > 0 {
		// Resolve tenant_id + flow_id + slug for the audit_log + SSE
		// publish. Single lookup; super-admin path so cross-tenant is
		// fine.
		var tenantID, flowID, slug string
		_ = s.deps.DB.DB.QueryRowContext(r.Context(),
			`SELECT d.tenant_id, d.flow_id, p.slug
			   FROM decisions d
			   JOIN project_versions pv ON pv.id = d.version_id
			   JOIN projects p ON p.id = pv.project_id
			  WHERE d.id = ?`,
			id,
		).Scan(&tenantID, &flowID, &slug)
		details, _ := json.Marshal(map[string]any{
			"decision_id":                id,
			"flow_id":                    flowID,
			"action":                     "admin_reactivate",
			"previous_superseded_by_id":  outcome.PreviousSupersededByID,
			"schema_ver":                 ProjectsSchemaVersion,
		})
		_ = s.deps.DB.WriteAudit(r.Context(), db.AuditEntry{
			ID:         uuid.NewString(),
			TS:         time.Now().UTC(),
			EventType:  MakeDecisionAuditEvent("admin_reactivate"),
			TenantID:   tenantID,
			UserID:     "", // super-admin handler doesn't carry the projects-claims context
			Method:     "POST",
			Endpoint:   r.URL.Path,
			StatusCode: http.StatusOK,
			IPAddress:  clientIP(r),
			Details:    string(details),
		})

		// Phase 5.2 P3 + 5.3 P1 — broadcast with chain delta. Mind-graph
		// (Phase 6) reads previous_superseded_by_id to erase the
		// supersession edge from this decision to its former successor.
		if s.deps.Broker != nil && tenantID != "" {
			s.deps.Broker.Publish(inboxBroadcastChannel(tenantID), sse.ProjectDecisionChanged{
				ProjectSlug:            slug,
				FlowID:                 flowID,
				DecisionID:             id,
				Tenant:                 tenantID,
				Status:                 "accepted",
				Action:                 "admin_reactivated",
				PreviousStatus:         "superseded",
				PreviousSupersededByID: outcome.PreviousSupersededByID,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"decision_id":                id,
		"updated":                    outcome.Updated,
		"previous_superseded_by_id":  outcome.PreviousSupersededByID,
	})
}

// HandleRecentDecisions serves GET /v1/atlas/admin/decisions/recent.
// Super-admin only — registered behind requireSuperAdmin in main.go.
func (s *Server) HandleRecentDecisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		_, _ = fmt.Sscanf(l, "%d", &n)
		if n > 0 {
			limit = n
		}
	}
	repoDB := NewDB(s.deps.DB.DB)
	out, err := repoDB.ListRecentDecisions(r.Context(), limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "recent_decisions", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"decisions": out,
		"count":     len(out),
	})
}

// ─── Phase 5 U1: DRD collab auth bridge ───────────────────────────────────

// HandleDRDTicket serves POST /v1/projects/{slug}/flows/{flow_id}/drd/ticket.
// Auth-gated; mints a single-use 60s ticket bound to (user, tenant, flow)
// for Hocuspocus' WebSocket handshake. Tenant + flow visibility checks
// happen here so the sidecar can trust the ticket without re-checking.
func (s *Server) HandleDRDTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if s.deps.Tickets == nil {
		writeJSONErr(w, http.StatusInternalServerError, "tickets_not_configured", "")
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
	flowID := r.PathValue("flow_id")
	if slug == "" || flowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.resolveDRDFlowID(r.Context(), slug, flowID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	// The DRD ticket's traceID is `drd:<flow_id>` so the existing
	// ticket store can scope it; the sidecar uses the same channel
	// when redeeming.
	traceID := "drd:" + flowID
	ticket := s.deps.Tickets.IssueTicket(claims.Sub, tenantID, traceID, sse.DefaultTicketTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"trace_id":   traceID,
		"flow_id":    flowID,
		"tenant_id":  tenantID,
		"user_id":    claims.Sub,
		"role":       claims.Role,
		"expires_in": int(sse.DefaultTicketTTL.Seconds()),
	})
}

// requireHocuspocusSecret is middleware-style: validates the
// X-DS-Hocuspocus-Secret header matches the env value. Used by the
// /internal/drd/* routes the sidecar calls.
func (s *Server) requireHocuspocusSecret(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := osGetenv(HocuspocusSharedSecretEnv)
		if want == "" {
			writeJSONErr(w, http.StatusInternalServerError, "secret_not_configured",
				"DS_HOCUSPOCUS_SHARED_SECRET env not set")
			return
		}
		got := r.Header.Get("X-DS-Hocuspocus-Secret")
		if got != want {
			writeJSONErr(w, http.StatusUnauthorized, "bad_secret", "")
			return
		}
		next(w, r)
	}
}

// HandleDRDInternalAuth serves POST /internal/drd/auth.
// Hocuspocus posts {ticket, flow_id} on each WebSocket handshake. We
// redeem + verify the ticket's flow matches, then return the user
// context + role so Hocuspocus can decide read-only vs. editor.
func (s *Server) HandleDRDInternalAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if s.deps.Tickets == nil {
		writeJSONErr(w, http.StatusInternalServerError, "tickets_not_configured", "")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req struct {
		Ticket string `json:"ticket"`
		FlowID string `json:"flow_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	if req.Ticket == "" || req.FlowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_fields", "ticket + flow_id required")
		return
	}
	userID, tenantID, traceID, ok := s.deps.Tickets.RedeemTicket(req.Ticket)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "invalid_ticket", "")
		return
	}
	wantTrace := "drd:" + req.FlowID
	if traceID != wantTrace {
		writeJSONErr(w, http.StatusForbidden, "wrong_channel", "ticket bound to a different flow")
		return
	}

	// Read the user's tenant role so Hocuspocus can gate read-only.
	role, err := s.deps.DB.GetTenantRole(r.Context(), tenantID, userID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "role_lookup", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":   userID,
		"tenant_id": tenantID,
		"flow_id":   req.FlowID,
		"role":      role,
	})
}

// HandleDRDInternalLoad serves GET /internal/drd/load?flow_id=...&tenant_id=...
// Hocuspocus calls this on first peer connect to bootstrap the Y.Doc
// from the persisted snapshot. Returns 200 + binary body when a snapshot
// exists, 200 + empty body otherwise.
func (s *Server) HandleDRDInternalLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	q := r.URL.Query()
	flowID := q.Get("flow_id")
	tenantID := q.Get("tenant_id")
	if flowID == "" || tenantID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_params", "flow_id + tenant_id required")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	state, err := repo.LoadYDocState(r.Context(), flowID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "load_ydoc", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	if state != nil {
		_, _ = w.Write(state)
	}
}

// HandleDRDInternalSnapshot serves POST /internal/drd/snapshot.
// Body shape: binary Y.Doc state. Headers carry flow_id + tenant_id +
// user_id + reason. Persists + writes audit_log + bumps revision.
func (s *Server) HandleDRDInternalSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	flowID := r.Header.Get("X-DS-Flow-ID")
	tenantID := r.Header.Get("X-DS-Tenant-ID")
	userID := r.Header.Get("X-DS-User-ID")
	reason := r.Header.Get("X-DS-Snapshot-Reason") // "idle" | "disconnect"
	if flowID == "" || tenantID == "" || userID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_headers",
			"X-DS-Flow-ID / X-DS-Tenant-ID / X-DS-User-ID required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxYDocBytes+1))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	if len(body) > MaxYDocBytes {
		writeJSONErr(w, http.StatusRequestEntityTooLarge, "ydoc_too_large",
			fmt.Sprintf("max %d bytes", MaxYDocBytes))
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	rev, err := repo.PersistYDocSnapshot(r.Context(), flowID, userID, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "persist", err.Error())
		return
	}

	// Audit-log the snapshot. flow_id + bytes + revision land in details
	// so the activity rail (U12) surfaces "DRD edit" entries.
	details, _ := json.Marshal(map[string]any{
		"flow_id":  flowID,
		"bytes":    len(body),
		"revision": rev,
		"reason":   reason,
	})
	_ = s.deps.DB.WriteAudit(r.Context(), db.AuditEntry{
		ID:         uuid.NewString(),
		TS:         time.Now().UTC(),
		EventType:  MakeDRDAuditEvent("snapshot"),
		TenantID:   tenantID,
		UserID:     userID,
		Method:     "POST",
		Endpoint:   "/internal/drd/snapshot",
		StatusCode: http.StatusOK,
		Details:    string(details),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"flow_id":  flowID,
		"revision": rev,
		"bytes":    len(body),
	})
}

// osGetenv is a tiny indirection so tests can inject a value via the
// ds-service test fixtures without importing os in every spec.
var osGetenv = os.Getenv

// HandleDRDInternalAuthGated etc. are exposed for cmd/server's route
// table. The bare HandleDRDInternalAuth method is unguarded so tests can
// drive it directly; production routing always goes through the gated
// wrapper which enforces the shared secret.
func (s *Server) HandleDRDInternalAuthGated() http.HandlerFunc {
	return s.requireHocuspocusSecret(s.HandleDRDInternalAuth)
}

func (s *Server) HandleDRDInternalLoadGated() http.HandlerFunc {
	return s.requireHocuspocusSecret(s.HandleDRDInternalLoad)
}

func (s *Server) HandleDRDInternalSnapshotGated() http.HandlerFunc {
	return s.requireHocuspocusSecret(s.HandleDRDInternalSnapshot)
}

// ─── Phase 5.2 P4: Figma frame metadata proxy ─────────────────────────────

// HandleFigmaFrameMetadata serves GET /v1/figma/frame-metadata?url=<figma_url>.
// Auth-gated, tenant-scoped. Parses the URL → calls Figma's /v1/images
// with the tenant's PAT → returns the signed CDN PNG URL + the frame
// title. 5min in-process cache keyed by (tenant_id, file_key, node_id).
//
// When the tenant has no PAT configured (FigmaPATResolver returns empty
// string with no error), the response falls back to URL-only metadata
// (title + node_id) without a thumbnail. Caller's UI keeps the gradient
// placeholder in that case.
func (s *Server) HandleFigmaFrameMetadata(w http.ResponseWriter, r *http.Request) {
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
	raw := r.URL.Query().Get("url")
	if raw == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_url", "?url= required")
		return
	}
	parsed, err := ParseFigmaURL(raw)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_figma_url", err.Error())
		return
	}

	cacheKey := figmaCacheKey(tenantID, parsed.FileKey, parsed.NodeID)
	if cached, ok := figmaProxy.get(cacheKey); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// Phase 5.3 P2 — per-tenant rate limit. Only enforced when we'd
	// actually call Figma's API; cache hits above bypass. 429 carries
	// Retry-After in seconds so clients back off gracefully.
	if s.deps.FigmaPATResolver != nil && parsed.NodeID != "" {
		if !figmaProxyLimiter.allow(tenantID, time.Now()) {
			w.Header().Set("Retry-After", "1")
			writeJSONErr(w, http.StatusTooManyRequests, "rate_limited",
				"figma proxy: too many requests for this tenant")
			return
		}
	}

	if s.deps.FigmaPATResolver != nil && parsed.NodeID != "" {
		pat, perr := s.deps.FigmaPATResolver(r.Context(), tenantID)
		if perr == nil && pat != "" {
			thumb, ferr := fetchFigmaThumbnailURL(r.Context(), pat, parsed.FileKey, parsed.NodeID)
			if ferr == nil {
				parsed.ThumbnailURL = thumb
				parsed.Source = "figma"
			} else if !errors.Is(ferr, errFigmaNotFound) {
				s.deps.Log.Warn("figma frame-metadata proxy failed", "error", ferr)
			}
		}
	}

	figmaProxy.set(cacheKey, parsed)
	writeJSON(w, http.StatusOK, parsed)
}

// ─── Phase 5 U12: activity rail ────────────────────────────────────────────

// FlowActivityEntry is one timeline row served by HandleFlowActivity. The
// shape mirrors db.AuditEntry but adds a parsed-out summary so the UI can
// render without a follow-up fetch.
type FlowActivityEntry struct {
	ID         string `json:"id"`
	TS         string `json:"ts"`
	EventType  string `json:"event_type"`
	UserID     string `json:"user_id"`
	Endpoint   string `json:"endpoint"`
	StatusCode int    `json:"status_code"`
	Details    string `json:"details,omitempty"`
}

// HandleFlowActivity serves GET /v1/projects/{slug}/flows/{flow_id}/activity.
// Returns a chronological timeline (newest first) of audit_log events
// scoped to this flow + the caller's tenant. Uses SQLite's json_extract to
// filter by flow_id inside the details column.
func (s *Server) HandleFlowActivity(w http.ResponseWriter, r *http.Request) {
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
	flowID := r.PathValue("flow_id")
	if slug == "" || flowID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	// Defense in depth: confirm the slug exists in the caller's tenant +
	// the flow lives inside that project. Wrong slug → 404 (no oracle).
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.GetProjectBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}
	if err := repo.assertFlowVisibleByID(r.Context(), flowID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "flow_lookup", err.Error())
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		_, _ = fmt.Sscanf(l, "%d", &n)
		if n > 0 && n <= 500 {
			limit = n
		}
	}

	rows, err := s.deps.DB.DB.QueryContext(r.Context(),
		`SELECT id, ts, event_type, user_id, endpoint, status_code, details
		   FROM audit_log
		  WHERE tenant_id = ?
		    AND json_valid(details)
		    AND json_extract(details, '$.flow_id') = ?
		  ORDER BY ts DESC
		  LIMIT ?`,
		tenantID, flowID, limit,
	)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "activity", err.Error())
		return
	}
	defer rows.Close()
	out := make([]FlowActivityEntry, 0, limit)
	for rows.Next() {
		var e FlowActivityEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.EventType, &e.UserID, &e.Endpoint, &e.StatusCode, &e.Details); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "scan", err.Error())
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"activity": out,
		"count":    len(out),
	})
}

// ─── Phase 5 U6+U7: comments + notifications ─────────────────────────────────

// createCommentRequest mirrors the JSON body for
// POST /v1/projects/{slug}/comments. The slug is required so we can
// confirm the flow belongs to this project before writing; target_kind +
// target_id discriminate which entity the comment hangs off.
type createCommentRequest struct {
	TargetKind      string `json:"target_kind"`
	TargetID        string `json:"target_id"`
	FlowID          string `json:"flow_id"`
	Body            string `json:"body"`
	ParentCommentID string `json:"parent_comment_id,omitempty"`
}

// HandleCommentCreate serves POST /v1/projects/{slug}/comments.
// Tenant-scoped; writes the comment + emits notification rows for each
// resolved @mention; broadcasts notification.created on the inbox SSE
// channel post-commit.
func (s *Server) HandleCommentCreate(w http.ResponseWriter, r *http.Request) {
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

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req createCommentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}

	in, err := ValidateCommentInput(CommentInput{
		TargetKind:      CommentTargetKind(req.TargetKind),
		TargetID:        req.TargetID,
		FlowID:          req.FlowID,
		Body:            req.Body,
		ParentCommentID: req.ParentCommentID,
	})
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_comment", err.Error())
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.GetProjectBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	rec, mentioned, err := repo.CreateComment(r.Context(), claims.Sub, in)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "create_comment", err.Error())
		return
	}

	// Best-effort SSE fan-out. Each mentioned user gets a notification
	// event on the tenant inbox channel — same channel Phase 4.1 ships.
	if s.deps.Broker != nil && len(mentioned) > 0 {
		ch := inboxBroadcastChannel(tenantID)
		for _, uid := range mentioned {
			s.deps.Broker.Publish(ch, sse.NotificationCreated{
				Tenant:          tenantID,
				RecipientUserID: uid,
				Kind:            string(NotifMention),
				TargetKind:      string(NotifTargetComment),
				TargetID:        rec.ID,
				FlowID:          rec.FlowID,
				ActorUserID:     claims.Sub,
			})
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"comment":           rec,
		"notified_user_ids": mentioned,
	})
}

// HandleCommentList serves GET /v1/projects/{slug}/comments?target_kind=&target_id=.
// Tenant-scoped; oldest first.
func (s *Server) HandleCommentList(w http.ResponseWriter, r *http.Request) {
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
	q := r.URL.Query()
	kind := q.Get("target_kind")
	target := q.Get("target_id")
	if kind == "" || target == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_target", "target_kind and target_id required")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	out, err := repo.ListCommentsForTarget(r.Context(), CommentTargetKind(kind), target)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_comments", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"comments": out,
		"count":    len(out),
	})
}

// HandleCommentResolve serves POST /v1/comments/{id}/resolve.
func (s *Server) HandleCommentResolve(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if id == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_id", "")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if err := repo.ResolveComment(r.Context(), id, claims.Sub); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "resolve_comment", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": true})
}

// HandleNotificationsList serves GET /v1/notifications?unread=1&limit=50.
func (s *Server) HandleNotificationsList(w http.ResponseWriter, r *http.Request) {
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
	q := r.URL.Query()
	unread := q.Get("unread") == "1"
	limit := 50
	if l := q.Get("limit"); l != "" {
		var n int
		_, _ = fmt.Sscanf(l, "%d", &n)
		if n > 0 {
			limit = n
		}
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	out, err := repo.ListNotificationsForUser(r.Context(), claims.Sub, unread, limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_notifications", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": out,
		"count":         len(out),
	})
}

// HandleNotificationsMarkRead serves POST /v1/notifications/mark-read with
// body `{ "ids": [...] }`. Caller-scoped; cross-recipient ids are silently
// ignored.
func (s *Server) HandleNotificationsMarkRead(w http.ResponseWriter, r *http.Request) {
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
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32*1024))
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "empty_ids", "")
		return
	}
	if len(req.IDs) > 200 {
		writeJSONErr(w, http.StatusBadRequest, "too_many_ids", "max 200")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	n, err := repo.MarkNotificationsRead(r.Context(), claims.Sub, req.IDs)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "mark_read", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// inboxBroadcastChannel returns the synthetic SSE channel used for
// tenant-scoped inbox broadcasts. Phase 4.1 — lifecycle events publish
// under both the project's trace_id (existing project Violations tab
// subscribers) AND this tenant channel (the /inbox cross-project view).
//
// The trace_id namespace is intentionally distinct from any real
// audit_jobs.trace_id so collisions are impossible.
func inboxBroadcastChannel(tenantID string) string {
	return "inbox:" + tenantID
}

// HandleInboxEventsTicket serves POST /v1/inbox/events/ticket.
//
// Mirrors HandleEventsTicket but binds the ticket to the synthetic
// inbox:<tenant_id> traceID instead of a project-specific one. The
// /inbox shell calls this on mount, redeems the ticket via
// EventSource(?ticket=…), and reconciles violation_lifecycle_changed
// events in place.
func (s *Server) HandleInboxEventsTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if s.deps.Tickets == nil {
		writeJSONErr(w, http.StatusInternalServerError, "tickets_not_configured", "")
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
	traceID := inboxBroadcastChannel(tenantID)
	ticket := s.deps.Tickets.IssueTicket(claims.Sub, tenantID, traceID, sse.DefaultTicketTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"trace_id":   traceID,
		"expires_in": int(sse.DefaultTicketTTL.Seconds()),
	})
}

// HandleInboxEvents serves GET /v1/inbox/events?ticket=...
// EventSource subscribes here for cross-project lifecycle updates. The
// stream stays open until the client disconnects; heartbeats keep the
// connection alive across proxies.
func (s *Server) HandleInboxEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tickets == nil || s.deps.Broker == nil {
		writeJSONErr(w, http.StatusInternalServerError, "sse_not_configured", "")
		return
	}
	if r.URL.Query().Get("token") != "" || r.URL.Query().Get("authorization") != "" {
		writeJSONErr(w, http.StatusBadRequest, "no_jwt_in_query", "use ?ticket=... not ?token=...")
		return
	}
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		writeJSONErr(w, http.StatusUnauthorized, "missing_ticket", "")
		return
	}
	userID, tenantID, traceID, ok := s.deps.Tickets.RedeemTicket(ticket)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "invalid_ticket", "")
		return
	}
	if traceID != inboxBroadcastChannel(tenantID) {
		writeJSONErr(w, http.StatusForbidden, "wrong_channel", "ticket bound to a non-inbox channel")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError, "no_streaming", "")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub, err := s.deps.Broker.Subscribe(traceID, tenantID, userID)
	if err != nil {
		if errors.Is(err, sse.ErrSubscriberCapReached) {
			writeJSONErr(w, http.StatusServiceUnavailable, "subscribers_full", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "subscribe", err.Error())
		return
	}
	defer unsub()

	clientGone := r.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case ev, alive := <-ch:
			if !alive {
				return
			}
			if sse.IsHeartbeat(ev) {
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
				continue
			}
			payload, _ := json.Marshal(ev.Payload())
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type(), payload)
			flusher.Flush()
		}
	}
}

// HandleViolationGet serves GET /v1/projects/{slug}/violations/{id}.
//
// Returns the violation + project + flow context the plugin needs to
// locate the offending node in Figma and render its auto-fix preview.
// Tenant-scoped (cross-tenant 404).
func (s *Server) HandleViolationGet(w http.ResponseWriter, r *http.Request) {
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
	violationID := r.PathValue("id")
	if slug == "" || violationID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	d, err := repo.GetViolation(r.Context(), slug, violationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// fixAppliedRequest is the body for POST /v1/projects/{slug}/violations/{id}/fix-applied.
type fixAppliedRequest struct {
	// Optional: free-text confirmation note the plugin may surface in
	// audit_log so retroactive auditors can see what shape the auto-fix
	// took ("Bound `colour.surface.button-cta` to fills[0]").
	Note string `json:"note,omitempty"`
}

// HandleViolationFixApplied serves POST
// /v1/projects/{slug}/violations/{id}/fix-applied.
//
// Plugin-only path: the deeplinked auto-fix flow calls this after the
// designer confirms + the plugin writes to the Figma file. Wraps U1's
// UpdateViolationStatus with action=mark_fixed (system-actor).
//
// Idempotency (per the plan): an already-Fixed violation returns 200,
// not 409. Auto-fix retries shouldn't trip the plugin into thinking a
// transient error landed.
func (s *Server) HandleViolationFixApplied(w http.ResponseWriter, r *http.Request) {
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
	violationID := r.PathValue("id")
	if slug == "" || violationID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req fixAppliedRequest
	_ = json.Unmarshal(body, &req)

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	current, err := repo.GetViolationForLifecycle(r.Context(), violationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}
	if current.ProjectSlug != slug {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	// Idempotency — already-fixed is a success.
	if current.From == ViolationStatusFixed {
		writeJSON(w, http.StatusOK, map[string]any{
			"violation_id": current.ViolationID,
			"status":       ViolationStatusFixed,
			"idempotent":   true,
		})
		return
	}

	transition, err := ValidateTransition(current.From, ActionMarkFixed, claims.Role, req.Note, true)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_transition", err.Error())
		return
	}

	now := time.Now().UTC()
	auditDetails, _ := json.Marshal(map[string]any{
		"violation_id": current.ViolationID,
		"version_id":   current.VersionID,
		"project_slug": current.ProjectSlug,
		"from":         transition.From,
		"to":           transition.To,
		"note":         req.Note,
		"fixed_via":    "auto-fix",
		"actor":        claims.Sub,
		"trace_id":     current.TraceID,
		"schema_ver":   ProjectsSchemaVersion,
	})
	auditWrite := func(tx *sql.Tx) error {
		_, ierr := tx.ExecContext(r.Context(),
			`INSERT INTO audit_log (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), now.Format(time.RFC3339Nano),
			transition.AuditEventType(),
			tenantID, claims.Sub,
			"POST",
			fmt.Sprintf("/v1/projects/%s/violations/%s/fix-applied", slug, violationID),
			http.StatusOK, 0, clientIP(r),
			string(auditDetails),
		)
		return ierr
	}

	if err := repo.UpdateViolationStatus(r.Context(), violationID, transition, auditWrite); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusConflict, "race", "violation status changed concurrently")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "update_violation", err.Error())
		return
	}

	if s.deps.Broker != nil {
		ev := sse.ProjectViolationLifecycleChanged{
			ProjectSlug: current.ProjectSlug,
			VersionID:   current.VersionID,
			ViolationID: current.ViolationID,
			Tenant:      tenantID,
			From:        transition.From,
			To:          transition.To,
			Action:      string(transition.Action),
			ActorUserID: claims.Sub,
		}
		if current.TraceID != "" {
			s.deps.Broker.Publish(current.TraceID, ev)
		}
		s.deps.Broker.Publish(inboxBroadcastChannel(tenantID), ev)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"violation_id": current.ViolationID,
		"status":       transition.To,
		"idempotent":   false,
	})
}

// HandleDashboardSummary serves GET /v1/atlas/admin/summary.
//
// Super-admin only — gated upstream by main.go's requireSuperAdmin
// middleware. Returns the five aggregations required for the DS-lead
// dashboard. ?weeks=4|8|12|24 controls the trend window.
func (s *Server) HandleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	weeks := 8
	if w := r.URL.Query().Get("weeks"); w != "" {
		var n int
		_, _ = fmt.Sscanf(w, "%d", &n)
		if n > 0 {
			weeks = n
		}
	}
	summary, err := BuildDashboardSummary(r.Context(), s.deps.DB.DB, weeks)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "dashboard", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// HandleComponentViolations serves GET /v1/components/violations?name=Toast.
//
// Returns the cross-tenant aggregate (severity tally + total + flow count)
// alongside the caller's tenant-scoped per-flow detail. The component is
// identified by its display name (mirrors what component_governance rules
// emit in the `observed` field). Phase 4 keeps this name-based to avoid
// pulling the lib/icons/manifest.json into the Go service; the frontend
// resolves slug → name from the manifest before calling.
func (s *Server) HandleComponentViolations(w http.ResponseWriter, r *http.Request) {
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
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_name", "?name= is required")
		return
	}

	isEditor := claims.Role == auth.RoleSuperAdmin
	if !isEditor {
		role, err := s.deps.DB.GetTenantRole(r.Context(), tenantID, claims.Sub)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "role_lookup", err.Error())
			return
		}
		switch role {
		case auth.RoleTenantAdmin, auth.RoleDesigner, auth.RoleEngineer:
			isEditor = true
		}
	}

	agg, flows, err := ComponentViolations(r.Context(), s.deps.DB.DB, tenantID, isEditor, claims.Sub, name)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "component_violations", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      name,
		"aggregate": agg,
		"flows":     flows,
	})
}

// HandleInbox serves GET /v1/inbox.
//
// Returns the requesting user's Active violations across every flow visible
// to them (Phase 4 visibility = project ownership OR designer-or-higher
// tenant role). Supports filters via query string:
//
//	?rule_id=X            single rule
//	?category=Y           single audit category
//	?persona_id=Z         persona UUID
//	?mode=light           mode_label exact match
//	?project_id=W         single project
//	?severity=critical    repeatable; OR'd
//	?date_from=RFC3339    inclusive lower bound on created_at
//	?date_to=RFC3339      inclusive upper bound
//	?limit=50             max MaxInboxLimit
//	?offset=0             pagination cursor
//
// Pagination is "Load more"-style — Phase 8 search replaces with a proper
// keyset cursor.
func (s *Server) HandleInbox(w http.ResponseWriter, r *http.Request) {
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

	// Resolve the caller's tenant role to decide whether they get the
	// editor-scope (sees every active violation) or the owner-scope
	// (only their own projects). super_admin always gets editor-scope.
	isEditor := claims.Role == auth.RoleSuperAdmin
	if !isEditor {
		role, err := s.deps.DB.GetTenantRole(r.Context(), tenantID, claims.Sub)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "role_lookup", err.Error())
			return
		}
		switch role {
		case auth.RoleTenantAdmin, auth.RoleDesigner, auth.RoleEngineer:
			isEditor = true
		}
	}

	q := r.URL.Query()
	filters := InboxFilters{
		RuleID:    strings.TrimSpace(q.Get("rule_id")),
		Category:  strings.TrimSpace(q.Get("category")),
		Persona:   strings.TrimSpace(q.Get("persona_id")),
		ModeLabel: strings.TrimSpace(q.Get("mode")),
		ProjectID: strings.TrimSpace(q.Get("project_id")),
	}
	if sevs, ok := q["severity"]; ok {
		for _, s := range sevs {
			s = strings.TrimSpace(s)
			if s != "" {
				filters.Severities = append(filters.Severities, s)
			}
		}
	}
	if df := q.Get("date_from"); df != "" {
		ts, err := time.Parse(time.RFC3339, df)
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "bad_date_from", err.Error())
			return
		}
		filters.DateFrom = ts
	}
	if dt := q.Get("date_to"); dt != "" {
		ts, err := time.Parse(time.RFC3339, dt)
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "bad_date_to", err.Error())
			return
		}
		filters.DateTo = ts
	}
	if l := q.Get("limit"); l != "" {
		var n int
		_, err := fmt.Sscanf(l, "%d", &n)
		if err != nil || n < 1 {
			writeJSONErr(w, http.StatusBadRequest, "bad_limit", "limit must be a positive integer")
			return
		}
		filters.Limit = n
	}
	if o := q.Get("offset"); o != "" {
		var n int
		_, err := fmt.Sscanf(o, "%d", &n)
		if err != nil || n < 0 {
			writeJSONErr(w, http.StatusBadRequest, "bad_offset", "offset must be >= 0")
			return
		}
		filters.Offset = n
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	rows, total, err := repo.GetInbox(r.Context(), claims.Sub, isEditor, filters)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "inbox", err.Error())
		return
	}
	limit := filters.Limit
	if limit <= 0 || limit > MaxInboxLimit {
		limit = DefaultInboxLimit
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":   rows,
		"total":  total,
		"limit":  limit,
		"offset": filters.Offset,
	})
}

// targetStatusFor returns the resulting violations.status for a given action
// when called against a row in "active" state. Used by the bulk SSE fan-out
// where re-running ValidateTransition per row would be wasted work — the bulk
// endpoint only emits events for rows we successfully UPDATEd, so the From
// is implicitly "active" (or "acknowledged"/"dismissed" for reactivate).
func targetStatusFor(a LifecycleAction) string {
	switch a {
	case ActionAcknowledge:
		return ViolationStatusAcknowledged
	case ActionDismiss:
		return ViolationStatusDismissed
	case ActionReactivate:
		return ViolationStatusActive
	case ActionMarkFixed:
		return ViolationStatusFixed
	}
	return ""
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// ctxKey type is unexported; must match cmd/server/main.go's literal-typed
// context key — registered via SetClaimsContextKey before mounting handlers.
type ctxKeyType string

// ctxKeyClaims is the projects-package context key. Handlers read claims
// inserted by the cmd/server middleware. Do not export — server registers a
// shim middleware that copies the cmd/server context value into ours.
const ctxKeyClaims ctxKeyType = "projects.claims"

// WithClaims returns a context with the given claims attached under the
// projects-package context key. cmd/server's middleware adapter calls this
// after JWT verification so handlers can read claims via r.Context().Value.
func WithClaims(ctx context.Context, c *auth.Claims) context.Context {
	return context.WithValue(ctx, ctxKeyClaims, c)
}

// AdaptAuthMiddleware lifts a cmd/server-style middleware into a projects
// handler. The cmd/server stores claims under its own ctxKey; this adapter
// re-reads + re-attaches them under the projects key. Production wiring uses
// it; tests call WithClaims directly.
func AdaptAuthMiddleware(reader func(*http.Request) *auth.Claims, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := reader(r)
		if claims == nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		ctx := WithClaims(r.Context(), claims)
		next(w, r.WithContext(ctx))
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{
		"error":  code,
		"detail": detail,
	})
}

// clientIP extracts the best-guess remote IP for audit_log purposes. Uses
// X-Forwarded-For first (assumes the deployment runs behind a TLS proxy that
// sets it correctly), falls back to RemoteAddr.
func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		// Take the leftmost (originator) entry.
		if idx := strings.Index(xf, ","); idx > 0 {
			return strings.TrimSpace(xf[:idx])
		}
		return strings.TrimSpace(xf)
	}
	return r.RemoteAddr
}

// _ time keeps the `time` import live in case future helpers add timestamps.
var _ = time.Now
