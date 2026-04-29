package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	MaxFramesPerFlow  = 50

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
			persona, err := repo.UpsertPersona(r.Context(), flow.PersonaName, claims.Sub)
			if err != nil {
				writeJSONErr(w, http.StatusInternalServerError, "upsert_persona", err.Error())
				return
			}
			id := persona.ID
			personaID = &id
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
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": p,
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
