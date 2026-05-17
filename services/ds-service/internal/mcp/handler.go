// Package mcp HTTP handler — mirrors auditbyslug's Deps + Handler factory
// shape (see internal/auditbyslug/handler.go).
//
// Two routes exposed via RegisterRoutes:
//   GET  /v1/mcp/tools           — cold catalog (Visible tools only).
//   POST /v1/mcp/invoke/{name}   — invoke any tool by name.
//
// Tenant scoping mirrors the projects subsystem: JWT claims are extracted
// at the HTTP boundary; a single-tenant claim resolves to projects.NewTenantRepoFromPool.
// Multi-tenant claims return 403 — Phase 1 doesn't support cross-tenant.
//
// IMPORTANT (per the U6 brief from the orchestrator): `RegisterRoutes`
// is intentionally NOT called from cmd/server/main.go in this unit. The
// wiring lands in the ship task so it doesn't collide with parallel
// session changes. The function exists so the ship task is a one-line edit.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// ClaimsReader is the same shape auditbyslug uses — keeps mcp decoupled
// from cmd/server's context-key plumbing.
type ClaimsReader func(r *http.Request) *auth.Claims

// HandlerDeps wires the http.Handler to its DB pool, SSE broker, claims
// reader, and the registry. Pass the pool + broker the rest of the server
// uses — this package owns no shared state of its own beyond the registry.
type HandlerDeps struct {
	DB           *db.DB
	Broker       *sse.MemoryBroker
	ClaimsReader ClaimsReader
	Registry     *Registry
	Log          *slog.Logger
}

// MaxInvokeBodyBytes caps the POST body size. ~1 MB is generous for any
// PRD edit (typical args are < 4 KB); the DRD ydoc snapshot path is
// capped server-side at 5 MB by projects.MaxYDocBytes anyway, but the
// HTTP layer caps lower to keep accidental megabyte uploads from a
// stray base64 paste off the disk path.
const MaxInvokeBodyBytes = 1 << 20 // 1 MiB

// RegisterRoutes wires the two MCP HTTP routes onto the supplied mux.
//
// Call site lives in the ship task — leaving this off main.go in U6 so
// parallel sessions don't fight over the same file.
func RegisterRoutes(mux *http.ServeMux, deps HandlerDeps, requireAuth func(http.HandlerFunc) http.HandlerFunc) {
	if deps.Registry == nil {
		panic("mcp: RegisterRoutes called with nil Registry")
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	mux.HandleFunc("GET /v1/mcp/tools", requireAuth(handleListTools(deps)))
	mux.HandleFunc("POST /v1/mcp/invoke/{name}", requireAuth(handleInvoke(deps)))
}

// ─── GET /v1/mcp/tools ─────────────────────────────────────────────────────

// catalogEntry is the wire shape for one tool in the cold catalog.
type catalogEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func handleListTools(deps HandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// We still verify a tenant — the cold catalog is identical per
		// tenant today, but rejecting unauthenticated callers keeps the
		// surface uniform with /invoke.
		if _, err := resolveTenant(deps.ClaimsReader, r); err != nil {
			writeErr(w, http.StatusForbidden, "no_tenant", err.Error())
			return
		}
		tools := deps.Registry.ListVisible()
		out := make([]catalogEntry, 0, len(tools))
		for _, t := range tools {
			out = append(out, catalogEntry{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=30")
		_ = json.NewEncoder(w).Encode(map[string]any{"tools": out})
	}
}

// ─── POST /v1/mcp/invoke/{name} ─────────────────────────────────────────────

func handleInvoke(deps HandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			writeErr(w, http.StatusBadRequest, "missing_tool_name", "")
			return
		}
		tenantID, err := resolveTenant(deps.ClaimsReader, r)
		if err != nil {
			writeErr(w, http.StatusForbidden, "no_tenant", err.Error())
			return
		}
		claims := deps.ClaimsReader(r)
		userID := ""
		if claims != nil {
			userID = claims.Sub
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, MaxInvokeBodyBytes+1))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "read_body", err.Error())
			return
		}
		if len(body) > MaxInvokeBodyBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("body exceeds %d bytes", MaxInvokeBodyBytes))
			return
		}

		// Empty body is allowed — tools that take no args accept null.
		var raw json.RawMessage = json.RawMessage(body)
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}

		// Per-request Deps: tenant-scoped repo, broker, user id.
		repo := projects.NewTenantRepoFromPool(deps.DB, tenantID)
		toolDeps := Deps{
			Repo:   repo,
			Broker: deps.Broker,
			UserID: userID,
		}

		result, invokeErr := deps.Registry.Invoke(r.Context(), name, toolDeps, raw)
		if invokeErr != nil {
			status, code := classifyError(invokeErr)
			deps.Log.Warn("mcp.invoke error",
				"tool", name,
				"tenant", tenantID,
				"status", status,
				"err", invokeErr.Error(),
			)
			writeErr(w, status, code, invokeErr.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

// resolveTenant extracts a single-tenant claim. Multi-tenant claims return
// an error — Phase 1 doesn't support cross-tenant invocation.
func resolveTenant(read ClaimsReader, r *http.Request) (string, error) {
	if read == nil {
		return "", errors.New("no claims reader configured")
	}
	c := read(r)
	if c == nil {
		return "", errors.New("missing claims")
	}
	switch len(c.Tenants) {
	case 0:
		return "", errors.New("user has no tenant")
	case 1:
		return c.Tenants[0], nil
	default:
		return "", errors.New("multi-tenant claims unsupported")
	}
}

// classifyError maps tool errors to HTTP status + machine-readable code.
// Falls back to 500 / "internal" for unknown errors.
func classifyError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrToolNotFound):
		return http.StatusNotFound, "tool_not_found"
	case errors.Is(err, ErrInvalidArgs):
		return http.StatusBadRequest, "invalid_args"
	case errors.Is(err, ErrNotImplemented):
		return http.StatusNotImplemented, "not_implemented"
	case errors.Is(err, projects.ErrNotFound),
		errors.Is(err, projects.ErrSubFlowNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, projects.ErrPRDInvalidInput),
		errors.Is(err, projects.ErrInvalidPrototypeURL):
		return http.StatusBadRequest, "invalid_input"
	}
	return http.StatusInternalServerError, "internal"
}

// writeErr is the canonical error responder. Mirrors auditbyslug's shape
// for FE consistency.
func writeErr(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  code,
		"detail": detail,
	})
}

// Ensure the context import is "used" by referring to it in a tiny
// compile-time-safe helper. Keeps `go vet` happy if a future refactor
// removes the in-line uses inside handleInvoke.
var _ context.Context = context.Background()
