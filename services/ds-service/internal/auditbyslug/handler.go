// Package auditbyslug implements GET /v1/audit/by-slug/:slug — Phase 2 U10.
//
// This is the runtime SQLite read path that replaces the build-time JSON
// sidecar import in app/files/[slug]/page.tsx. The response shape mirrors
// audit.AuditResult exactly (= lib/audit/types.ts AuditResult on the FE),
// so the frontend doesn't change types.
//
// Tenant scoping:
//   - Reads the JWT bearer claims attached by cmd/server's requireAuth.
//   - tenant_id comes from the claim. Cross-tenant slug returns 404 — the
//     same "no existence oracle" invariant Phase 1 established (see
//     internal/projects/server.go HandleProjectGet).
//   - Sidecar-backfilled rows live under the system tenant (DS_SYSTEM_TENANT_ID,
//     default "system"; see internal/projects/backfill.go). For those slugs
//     the handler also checks the system tenant when the caller's tenant
//     query misses, so the docs-site /files/<slug> route keeps rendering
//     the org-wide synthetic projects without giving any one tenant write
//     authority over them. Toggle via DS_AUDIT_BY_SLUG_INCLUDE_SYSTEM=0 to
//     scope strictly to the caller's tenant.
//
// Pending / failed versions:
//   - If the latest project_versions row is `pending` or `failed`, we return
//     503 Service Unavailable — the audit data isn't ready yet. Frontend can
//     show a "still computing" state. We chose 503 over 425 (Too Early) because
//     409/425 imply client retry intent that doesn't apply here; 503 with a
//     Retry-After is the closest semantic match for "data exists but isn't
//     in a serveable state".
package auditbyslug

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// ClaimsReader extracts auth claims from the request — same shape the
// projects package's AdaptAuthMiddleware uses. Keeps this package decoupled
// from cmd/server's context-key plumbing.
type ClaimsReader func(r *http.Request) *auth.Claims

// Deps wires the handler to its DB and logger.
type Deps struct {
	DB             *sql.DB
	ClaimsReader   ClaimsReader
	Log            *slog.Logger
	SystemTenantID string // optional override; defaults to env DS_SYSTEM_TENANT_ID or "system"
	IncludeSystem  *bool  // optional override; when nil reads DS_AUDIT_BY_SLUG_INCLUDE_SYSTEM
}

// Handler returns the http.HandlerFunc for GET /v1/audit/by-slug/{slug}.
// Wire behind requireAuth in cmd/server/main.go.
func Handler(deps Deps) http.HandlerFunc {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.SystemTenantID == "" {
		if v := os.Getenv("DS_SYSTEM_TENANT_ID"); v != "" {
			deps.SystemTenantID = v
		} else {
			deps.SystemTenantID = "system"
		}
	}
	includeSystem := true
	if deps.IncludeSystem != nil {
		includeSystem = *deps.IncludeSystem
	} else if v := os.Getenv("DS_AUDIT_BY_SLUG_INCLUDE_SYSTEM"); v == "0" || strings.EqualFold(v, "false") {
		includeSystem = false
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims := deps.ClaimsReader(r)
		if claims == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		tenantID := resolveTenantID(claims)
		if tenantID == "" {
			writeErr(w, http.StatusForbidden, "no_tenant", "user has no resolvable tenant")
			return
		}

		slug := r.PathValue("slug")
		if !validSlug(slug) {
			writeErr(w, http.StatusBadRequest, "invalid_slug", "")
			return
		}

		ctx := r.Context()

		// Tenant-scoped lookup first.
		result, err := loadAuditBySlug(ctx, deps.DB, slug, tenantID)
		if err != nil {
			if !errors.Is(err, errNotFound) && !errors.Is(err, errVersionNotReady) {
				deps.Log.Error("audit by-slug: tenant lookup", "err", err, "tenant", tenantID, "slug", slug)
				writeErr(w, http.StatusInternalServerError, "lookup", "")
				return
			}
		}

		// If the caller's tenant doesn't have it AND we're configured to fall
		// through to the system tenant for sidecar-backfilled rows, try there.
		if errors.Is(err, errNotFound) && includeSystem && tenantID != deps.SystemTenantID {
			result, err = loadAuditBySlug(ctx, deps.DB, slug, deps.SystemTenantID)
			if err != nil && !errors.Is(err, errNotFound) && !errors.Is(err, errVersionNotReady) {
				deps.Log.Error("audit by-slug: system lookup", "err", err, "slug", slug)
				writeErr(w, http.StatusInternalServerError, "lookup", "")
				return
			}
		}

		if errors.Is(err, errNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		if errors.Is(err, errVersionNotReady) {
			// Inline rationale: see package doc comment. 503 + Retry-After.
			w.Header().Set("Retry-After", "30")
			writeErr(w, http.StatusServiceUnavailable, "version_not_ready", "audit pipeline still in progress")
			return
		}

		// Success — write the AuditResult JSON. Cache hint: results are
		// per-version snapshots; pin to a short private cache so a subsequent
		// re-export bumps quickly.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=30")
		_ = json.NewEncoder(w).Encode(result)
	}
}

// resolveTenantID mirrors projects.Server.resolveTenantID — single-tenant
// claims only in Phase 1/2.
func resolveTenantID(c *auth.Claims) string {
	if c == nil {
		return ""
	}
	if len(c.Tenants) == 1 {
		return c.Tenants[0]
	}
	return ""
}

// validSlug applies the same allowlist the existing TS loader uses
// (lib/audit/files.ts): kebab-case ASCII alphanumerics and hyphens.
func validSlug(s string) bool {
	if s == "" || len(s) > 80 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

var (
	errNotFound        = errors.New("auditbyslug: not found")
	errVersionNotReady = errors.New("auditbyslug: version not ready")
)

// loadAuditBySlug assembles an audit.AuditResult from the SQLite tables
// (projects × project_versions × screens × violations) for the given tenant.
// Returns errNotFound when no project for (slug, tenantID) exists, or no
// version exists yet. Returns errVersionNotReady when the latest version is
// pending or failed.
func loadAuditBySlug(ctx context.Context, db *sql.DB, slug, tenantID string) (*audit.AuditResult, error) {
	// 1. Project row.
	var (
		projectID  string
		name       string
		platform   string
		product    string
		path       string
		updatedAt  string
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, name, platform, product, path, updated_at
		  FROM projects
		 WHERE slug = ? AND tenant_id = ? AND deleted_at IS NULL
	`, slug, tenantID).Scan(&projectID, &name, &platform, &product, &path, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project lookup: %w", err)
	}

	// 2. Latest version (highest version_index).
	var (
		versionID    string
		versionIndex int
		status       string
		createdAt    string
	)
	err = db.QueryRowContext(ctx, `
		SELECT id, version_index, status, created_at
		  FROM project_versions
		 WHERE project_id = ? AND tenant_id = ?
		 ORDER BY version_index DESC
		 LIMIT 1
	`, projectID, tenantID).Scan(&versionID, &versionIndex, &status, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("version lookup: %w", err)
	}
	if status != "view_ready" {
		// pending | failed — not serveable yet.
		return nil, errVersionNotReady
	}

	// 3. Screens for the version (preserve insertion order — screens.created_at
	//    is per-row but for backfill all share the same timestamp; use rowid
	//    order which matches insertion for our cases).
	rows, err := db.QueryContext(ctx, `
		SELECT id, screen_logical_id
		  FROM screens
		 WHERE version_id = ? AND tenant_id = ?
		 ORDER BY rowid ASC
	`, versionID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("screens query: %w", err)
	}
	defer rows.Close()

	type screenRow struct {
		ID       string
		NodeID   string
		FixCount int
	}
	var screenList []*screenRow
	screenByID := map[string]*screenRow{}
	for rows.Next() {
		s := &screenRow{}
		if err := rows.Scan(&s.ID, &s.NodeID); err != nil {
			return nil, fmt.Errorf("scan screen: %w", err)
		}
		screenList = append(screenList, s)
		screenByID[s.ID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("screens iter: %w", err)
	}

	// 4. Violations for the version, grouped by screen.
	vrows, err := db.QueryContext(ctx, `
		SELECT screen_id, rule_id, severity, property, observed, suggestion
		  FROM violations
		 WHERE version_id = ? AND tenant_id = ?
		 ORDER BY rowid ASC
	`, versionID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("violations query: %w", err)
	}
	defer vrows.Close()

	fixesByScreen := map[string][]audit.FixCandidate{}
	for vrows.Next() {
		var (
			screenID, ruleID, severity, property string
			observed, suggestion                 sql.NullString
		)
		if err := vrows.Scan(&screenID, &ruleID, &severity, &property, &observed, &suggestion); err != nil {
			return nil, fmt.Errorf("scan violation: %w", err)
		}
		fc := violationToFixCandidate(ruleID, severity, property, observed.String, suggestion.String)
		fixesByScreen[screenID] = append(fixesByScreen[screenID], fc)
	}
	if err := vrows.Err(); err != nil {
		return nil, fmt.Errorf("violations iter: %w", err)
	}

	// 5. Reconstruct AuditScreen rows. Coverage / component_summary aren't
	//    persisted in U9's backfill schema — emit zero-valued structs so the
	//    JSON shape stays valid and the frontend renders without nil-checks.
	auditScreens := make([]audit.AuditScreen, 0, len(screenList))
	for _, s := range screenList {
		fixes := fixesByScreen[s.ID]
		if fixes == nil {
			fixes = []audit.FixCandidate{}
		}
		auditScreens = append(auditScreens, audit.AuditScreen{
			NodeID: s.NodeID,
			Name:   s.NodeID, // backfill doesn't preserve frame name; node_id is the closest
			Slug:   s.ID,
			Fixes:  fixes,
			ComponentMatches: []audit.ComponentMatch{},
		})
	}

	// 6. Top-level AuditResult. Most aggregate fields aren't reconstructable
	//    from screens+violations alone — emit safe defaults so the schema-
	//    version handshake on the FE keeps working. SchemaVersion stays "1.0".
	updated := parseTime(updatedAt)
	created := parseTime(createdAt)
	extracted := updated
	if extracted.IsZero() {
		extracted = created
	}
	if extracted.IsZero() {
		extracted = time.Now().UTC()
	}

	return &audit.AuditResult{
		SchemaVersion:   audit.SchemaVersion,
		FileKey:         projectID,
		FileName:        name,
		FileSlug:        slug,
		Brand:           "indmoney",
		ExtractedAt:     extracted,
		DesignSystemRev: "",
		Screens:         auditScreens,
	}, nil
}

// violationToFixCandidate is the inverse of internal/projects/backfill.go's
// violation insert: rule_id is "<reason>.<property>", suggestion is either
// "Bind to <token_path>" or "Replace deprecated token with <replaced_by>" or
// the raw rationale. Severity → priority is the inverse of MapPriorityToSeverity.
func violationToFixCandidate(ruleID, severity, property, observed, suggestion string) audit.FixCandidate {
	reason := ""
	if dot := strings.IndexByte(ruleID, '.'); dot > 0 {
		reason = ruleID[:dot]
	}
	fc := audit.FixCandidate{
		Property: property,
		Observed: observed,
		Reason:   reason,
		Priority: severityToPriority(severity, reason),
	}
	switch {
	case strings.HasPrefix(suggestion, "Bind to "):
		fc.TokenPath = strings.TrimPrefix(suggestion, "Bind to ")
	case strings.HasPrefix(suggestion, "Replace deprecated token with "):
		fc.ReplacedBy = strings.TrimPrefix(suggestion, "Replace deprecated token with ")
	case suggestion != "":
		fc.Rationale = suggestion
	}
	return fc
}

// severityToPriority is the lossy inverse of runner.MapPriorityToSeverity.
// We can't always recover the original priority (P1 deprecated and P1 drift
// both came in as different severities), so map by the dominant rule.
func severityToPriority(severity, reason string) audit.Priority {
	switch severity {
	case "critical":
		return audit.PriorityP1
	case "high":
		return audit.PriorityP1
	case "medium":
		return audit.PriorityP2
	case "low":
		return audit.PriorityP3
	case "info":
		return audit.PriorityP3
	}
	_ = reason
	return audit.PriorityP3
}

// parseTime tolerates RFC3339 + RFC3339Nano (mirror of projects.parseTime).
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// writeErr matches the projects package's writeJSONErr response shape so
// frontend error handling is consistent across endpoints.
func writeErr(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  code,
		"detail": detail,
	})
}
