// Tests for GET /v1/audit/by-slug/:slug — Phase 2 U10.
//
// Pattern: same temp-DB setup as internal/projects/repository_test.go's
// newTestDB helper. We seed projects/versions/screens/violations directly
// via SQL so the test doesn't pull in the projects-package upsert paths
// (which carry validation that's tangential to this handler's contract).

package auditbyslug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// newTestDB seeds two tenants + one user the same way
// internal/projects/repository_test.go's helper does. Returns (db, tenantA, tenantB, userA).
func newTestDB(t *testing.T) (*db.DB, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	ctx := context.Background()
	userA := uuid.NewString()
	if err := d.CreateUser(ctx, db.User{ID: userA, Email: "a@example.com",
		PasswordHash: "x", Role: "user", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create userA: %v", err)
	}
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	if err := d.CreateTenant(ctx, db.Tenant{ID: tenantA, Slug: "tenant-a", Name: "A",
		Status: "active", PlanType: "free", CreatedAt: time.Now(), CreatedBy: userA}); err != nil {
		t.Fatalf("create tenantA: %v", err)
	}
	if err := d.CreateTenant(ctx, db.Tenant{ID: tenantB, Slug: "tenant-b", Name: "B",
		Status: "active", PlanType: "free", CreatedAt: time.Now(), CreatedBy: userA}); err != nil {
		t.Fatalf("create tenantB: %v", err)
	}
	return d, tenantA, tenantB, userA
}

// seed inserts a project + version + N screens + violations directly. status
// defaults to "view_ready" unless overridden.
type seedFix struct {
	RuleID     string
	Severity   string
	Property   string
	Observed   string
	Suggestion string
}
type seedScreen struct {
	NodeID string
	Fixes  []seedFix
}
type seedSpec struct {
	TenantID    string
	UserID      string
	Slug        string
	Name        string
	Status      string // "" → "view_ready"
	Screens     []seedScreen
}

func seedProject(t *testing.T, d *db.DB, spec seedSpec) (projectID, versionID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	projectID = uuid.NewString()
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
		 VALUES (?, ?, ?, 'web', 'DesignSystem', ?, ?, ?, ?, ?)`,
		projectID, spec.Slug, spec.Name, "docs/"+spec.Slug, spec.UserID, spec.TenantID, now, now,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	flowID := uuid.NewString()
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		flowID, projectID, spec.TenantID, "sidecar:"+spec.Slug, "Components", now, now,
	); err != nil {
		t.Fatalf("insert flow: %v", err)
	}

	status := spec.Status
	if status == "" {
		status = "view_ready"
	}
	versionID = uuid.NewString()
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?)`,
		versionID, projectID, spec.TenantID, status, spec.UserID, now,
	); err != nil {
		t.Fatalf("insert version: %v", err)
	}

	for i, sc := range spec.Screens {
		screenID := uuid.NewString()
		if _, err := d.DB.ExecContext(ctx,
			`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			screenID, versionID, flowID, spec.TenantID,
			0.0, float64(i*1000), 1024.0, 800.0, sc.NodeID, now,
		); err != nil {
			t.Fatalf("insert screen: %v", err)
		}
		for _, f := range sc.Fixes {
			if _, err := d.DB.ExecContext(ctx,
				`INSERT INTO violations
					(id, version_id, screen_id, tenant_id, rule_id, severity, category,
					 property, observed, suggestion, status, auto_fixable, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 0, ?)`,
				uuid.NewString(), versionID, screenID, spec.TenantID,
				f.RuleID, f.Severity, "token_drift",
				f.Property, f.Observed, f.Suggestion, now,
			); err != nil {
				t.Fatalf("insert violation: %v", err)
			}
		}
	}
	return projectID, versionID
}

// fakeClaims returns a ClaimsReader that always yields the given tenant/user.
func fakeClaims(tenantID, userID string) ClaimsReader {
	return func(_ *http.Request) *auth.Claims {
		return &auth.Claims{Sub: userID, Tenants: []string{tenantID}}
	}
}

func TestHandler(t *testing.T) {
	d, tenantA, tenantB, userA := newTestDB(t)

	// ── seed data ────────────────────────────────────────────────────────────
	// Tenant A: "happy" slug with two screens and three fixes.
	seedProject(t, d, seedSpec{
		TenantID: tenantA, UserID: userA, Slug: "happy", Name: "Happy",
		Screens: []seedScreen{
			{NodeID: "1:1", Fixes: []seedFix{
				{RuleID: "drift.fill", Severity: "high", Property: "fill",
					Observed: "#6B7280", Suggestion: "Bind to surface.surface-grey-bg"},
				{RuleID: "deprecated.text", Severity: "critical", Property: "text",
					Observed: "Inter/14", Suggestion: "Replace deprecated token with body-md"},
			}},
			{NodeID: "1:2", Fixes: []seedFix{
				{RuleID: "drift.padding", Severity: "medium", Property: "padding",
					Observed: "13px", Suggestion: "Bind to spacing.sp-12"},
			}},
		},
	})

	// Tenant A: "empty" slug — zero violations.
	seedProject(t, d, seedSpec{
		TenantID: tenantA, UserID: userA, Slug: "empty", Name: "Empty",
		Screens: []seedScreen{{NodeID: "2:1"}},
	})

	// Tenant B: "tenant-b-only" slug — used to assert cross-tenant 404.
	seedProject(t, d, seedSpec{
		TenantID: tenantB, UserID: userA, Slug: "tenant-b-only", Name: "B-Only",
		Screens: []seedScreen{{NodeID: "3:1"}},
	})

	includeSystemFalse := false
	mkHandler := func(claims ClaimsReader) http.HandlerFunc {
		return Handler(Deps{
			DB:            d.DB,
			ClaimsReader:  claims,
			IncludeSystem: &includeSystemFalse, // strict tenant scoping for these tests
		})
	}

	// We register the handler at the same path Go 1.22+ ServeMux uses to
	// expose the {slug} path-value via r.PathValue.
	mkServer := func(claims ClaimsReader) *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/audit/by-slug/{slug}", mkHandler(claims))
		return mux
	}

	type want struct {
		status        int
		errorCode     string // for non-2xx
		fileSlug      string
		screenCount   int
		totalFixes    int
	}

	cases := []struct {
		name   string
		claims ClaimsReader
		slug   string
		want   want
	}{
		{
			name:   "happy path: existing slug returns full payload",
			claims: fakeClaims(tenantA, userA),
			slug:   "happy",
			want:   want{status: 200, fileSlug: "happy", screenCount: 2, totalFixes: 3},
		},
		{
			name:   "404 on non-existent slug",
			claims: fakeClaims(tenantA, userA),
			slug:   "does-not-exist",
			want:   want{status: 404, errorCode: "not_found"},
		},
		{
			name:   "cross-tenant returns 404 (no existence oracle)",
			claims: fakeClaims(tenantA, userA),
			slug:   "tenant-b-only",
			want:   want{status: 404, errorCode: "not_found"},
		},
		{
			name:   "empty violation set still returns valid AuditResult",
			claims: fakeClaims(tenantA, userA),
			slug:   "empty",
			want:   want{status: 200, fileSlug: "empty", screenCount: 1, totalFixes: 0},
		},
		{
			name:   "missing claims returns 401",
			claims: func(_ *http.Request) *auth.Claims { return nil },
			slug:   "happy",
			want:   want{status: 401, errorCode: "unauthorized"},
		},
		{
			name: "no resolvable tenant returns 403",
			claims: func(_ *http.Request) *auth.Claims {
				return &auth.Claims{Sub: userA, Tenants: nil}
			},
			slug: "happy",
			want: want{status: 403, errorCode: "no_tenant"},
		},
		{
			// "Bad..Slug" is a single path segment so it reaches the handler;
			// the slug regex rejects it (dots aren't allowed in the
			// kebab-case-only allowlist).
			name:   "invalid slug returns 400",
			claims: fakeClaims(tenantA, userA),
			slug:   "Bad..Slug",
			want:   want{status: 400, errorCode: "invalid_slug"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := mkServer(tc.claims)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/audit/by-slug/"+tc.slug, nil)
			srv.ServeHTTP(rec, req)

			if rec.Code != tc.want.status {
				t.Fatalf("status: want %d, got %d (body=%s)", tc.want.status, rec.Code, rec.Body.String())
			}

			if tc.want.status != 200 {
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode error body: %v (raw=%s)", err, rec.Body.String())
				}
				if got, _ := body["error"].(string); got != tc.want.errorCode {
					t.Fatalf("error code: want %q, got %q (body=%s)", tc.want.errorCode, got, rec.Body.String())
				}
				return
			}

			var result audit.AuditResult
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode AuditResult: %v (raw=%s)", err, rec.Body.String())
			}
			if result.SchemaVersion != audit.SchemaVersion {
				t.Errorf("schema_version: want %q, got %q", audit.SchemaVersion, result.SchemaVersion)
			}
			if result.FileSlug != tc.want.fileSlug {
				t.Errorf("file_slug: want %q, got %q", tc.want.fileSlug, result.FileSlug)
			}
			if len(result.Screens) != tc.want.screenCount {
				t.Errorf("screen count: want %d, got %d", tc.want.screenCount, len(result.Screens))
			}
			total := 0
			for _, s := range result.Screens {
				total += len(s.Fixes)
			}
			if total != tc.want.totalFixes {
				t.Errorf("total fixes: want %d, got %d", tc.want.totalFixes, total)
			}
		})
	}
}

func TestHandler_PendingVersionReturns503(t *testing.T) {
	d, tenantA, _, userA := newTestDB(t)

	seedProject(t, d, seedSpec{
		TenantID: tenantA, UserID: userA, Slug: "still-running", Name: "Pending",
		Status:   "pending",
		Screens:  []seedScreen{{NodeID: "9:1"}},
	})

	includeSystemFalse := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/audit/by-slug/{slug}", Handler(Deps{
		DB:            d.DB,
		ClaimsReader:  fakeClaims(tenantA, userA),
		IncludeSystem: &includeSystemFalse,
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/by-slug/still-running", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.EqualFold(rec.Header().Get("Retry-After"), "30") {
		t.Errorf("expected Retry-After: 30, got %q", rec.Header().Get("Retry-After"))
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, _ := body["error"].(string); got != "version_not_ready" {
		t.Errorf("error code: want version_not_ready, got %q", got)
	}
}

func TestHandler_FixCandidateReversal(t *testing.T) {
	// Sanity-check the inverse mapping of backfill.go's
	// suggestion-and-rule-id encoding so the FE FixCandidate carries
	// token_path / replaced_by / rationale in the same fields.
	d, tenantA, _, userA := newTestDB(t)
	seedProject(t, d, seedSpec{
		TenantID: tenantA, UserID: userA, Slug: "rev", Name: "Reversal",
		Screens: []seedScreen{{NodeID: "1:1", Fixes: []seedFix{
			{RuleID: "drift.fill", Severity: "high", Property: "fill",
				Observed: "#abc", Suggestion: "Bind to color.brand-primary"},
			{RuleID: "deprecated.fill", Severity: "critical", Property: "fill",
				Observed: "#def", Suggestion: "Replace deprecated token with color.brand-secondary"},
			{RuleID: "drift.spacing", Severity: "low", Property: "padding",
				Observed: "11px", Suggestion: "Designer-authored note"},
		}}},
	})

	includeSystemFalse := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/audit/by-slug/{slug}", Handler(Deps{
		DB:            d.DB,
		ClaimsReader:  fakeClaims(tenantA, userA),
		IncludeSystem: &includeSystemFalse,
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/by-slug/rev", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var result audit.AuditResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Screens) != 1 || len(result.Screens[0].Fixes) != 3 {
		t.Fatalf("unexpected screens/fixes shape: %+v", result.Screens)
	}
	fixes := result.Screens[0].Fixes
	if fixes[0].TokenPath != "color.brand-primary" {
		t.Errorf("fix[0].token_path: %q", fixes[0].TokenPath)
	}
	if fixes[1].ReplacedBy != "color.brand-secondary" {
		t.Errorf("fix[1].replaced_by: %q", fixes[1].ReplacedBy)
	}
	if fixes[2].Rationale != "Designer-authored note" {
		t.Errorf("fix[2].rationale: %q", fixes[2].Rationale)
	}
	if fixes[0].Reason != "drift" || fixes[1].Reason != "deprecated" {
		t.Errorf("reason mapping: %+v / %+v", fixes[0].Reason, fixes[1].Reason)
	}
}
