package projects

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	dbpkg "github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// pngHandlerFixture wires the minimum dependencies the U11 handler needs:
// a fresh DB with the projects schema, a screen row + persisted PNG file under
// data/screens/<tenant>/<version>/<screen>@2x.png, and a *Server backed by
// these.
type pngHandlerFixture struct {
	server  *Server
	dataDir string
	repo    *TenantRepo
	tenantA string
	tenantB string
	user    string
	slug    string
	version string
	screen  string
	pngPath string
	pngKey  string
	pngBody []byte
}

func newPNGHandlerFixture(t *testing.T) *pngHandlerFixture {
	t.Helper()

	d, err := dbpkg.Open(t.TempDir() + "/png-test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	dataDir := t.TempDir()

	tenantA := "tenant-a"
	tenantB := "tenant-b"
	userID := "user-1"
	now := time.Now().UTC().Format(time.RFC3339)

	// Seed users + tenants so FKs are satisfied.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, email, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{userID, "u@x.com", "h", "user", now}},
		{`INSERT INTO tenants (id, slug, name, status, plan_type, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{tenantA, "t-a", "Tenant A", "active", "free", now, userID}},
		{`INSERT INTO tenants (id, slug, name, status, plan_type, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{tenantB, "t-b", "Tenant B", "active", "free", now, userID}},
	} {
		if _, err := d.ExecContext(context.Background(), q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	repo := NewTenantRepo(d.DB, tenantA)

	// Seed a project + version + flow + screen for tenant A.
	projectID := uuid.NewString()
	versionID := uuid.NewString()
	flowID := uuid.NewString()
	screenID := uuid.NewString()
	slug := "test-flow"
	// Match production's persistPNG output: storage key includes the
	// "screens/" prefix so the handler can resolve it relative to DataDir
	// without an extra Join.
	pngKey := "screens/" + tenantA + "/" + versionID + "/" + screenID + "@2x.png"

	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{projectID, slug, "Test Flow", "mobile", "Indian Stocks",
				"Indian Stocks/Test", userID, tenantA, now, now}},
		{`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		  VALUES (?, ?, ?, 1, 'view_ready', ?, ?)`,
			[]any{versionID, projectID, tenantA, userID, now}},
		{`INSERT INTO flows (id, project_id, tenant_id, file_id, name, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{flowID, projectID, tenantA, "file-A", "Flow", now, now}},
		{`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, png_storage_key, created_at)
		  VALUES (?, ?, ?, ?, 0, 0, 375, 812, ?, ?, ?)`,
			[]any{screenID, versionID, flowID, tenantA, "logical-1", pngKey, now}},
	} {
		if _, err := d.ExecContext(context.Background(), q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Render a tiny 8×8 red PNG and write to disk at the expected key path.
	// pngKey already begins with "screens/"; just join under DataDir.
	pngPath := filepath.Join(dataDir, pngKey)
	if err := os.MkdirAll(filepath.Dir(pngPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	if err := os.WriteFile(pngPath, pngBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	server := NewServer(ServerDeps{DB: d, DataDir: dataDir})

	return &pngHandlerFixture{
		server: server, dataDir: dataDir, repo: repo,
		tenantA: tenantA, tenantB: tenantB, user: userID,
		slug: slug, version: versionID, screen: screenID,
		pngPath: pngPath, pngKey: pngKey, pngBody: pngBuf.Bytes(),
	}
}

// pngRequest builds a GET request with the projects-package claims-key set in
// context, ready for r.PathValue. Reuses requestWithClaims from server_test.go
// for context wiring (passes nil body since GET has none).
func pngRequest(target string, claims *auth.Claims, slug, screenID string) *http.Request {
	req := requestWithClaims("GET", target, nil, claims)
	req.SetPathValue("slug", slug)
	req.SetPathValue("id", screenID)
	return req
}

func TestHandleScreenPNG_HappyPath(t *testing.T) {
	f := newPNGHandlerFixture(t)
	claims := &auth.Claims{Sub: f.user, Tenants: []string{f.tenantA}}
	req := pngRequest("/v1/projects/"+f.slug+"/screens/"+f.screen+"/png", claims, f.slug, f.screen)

	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Errorf("Cache-Control = %q, want private,max-age=300", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want inline", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), f.pngBody) {
		t.Errorf("body bytes do not match persisted PNG")
	}
}

func TestHandleScreenPNG_Unauthenticated_401(t *testing.T) {
	f := newPNGHandlerFixture(t)
	// No claims in context.
	req := pngRequest("/v1/projects/"+f.slug+"/screens/"+f.screen+"/png", nil, f.slug, f.screen)
	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestHandleScreenPNG_CrossTenant_404(t *testing.T) {
	f := newPNGHandlerFixture(t)
	// User authenticated with tenant B asking for tenant A's screen → 404 (no existence oracle).
	claims := &auth.Claims{Sub: f.user, Tenants: []string{f.tenantB}}
	req := pngRequest("/v1/projects/"+f.slug+"/screens/"+f.screen+"/png", claims, f.slug, f.screen)
	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant: got %d, want 404 (NOT 403)", rec.Code)
	}
}

func TestHandleScreenPNG_FileMissing_404(t *testing.T) {
	f := newPNGHandlerFixture(t)
	// Delete the file on disk to simulate ops removing it. DB row still
	// references the storage key.
	if err := os.Remove(f.pngPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	claims := &auth.Claims{Sub: f.user, Tenants: []string{f.tenantA}}
	req := pngRequest("/v1/projects/"+f.slug+"/screens/"+f.screen+"/png", claims, f.slug, f.screen)
	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing file: got %d, want 404", rec.Code)
	}
}

func TestHandleScreenPNG_NoPngStorageKey_404(t *testing.T) {
	f := newPNGHandlerFixture(t)
	// Clear the storage key to simulate a screen whose PNG hasn't rendered yet.
	if _, err := f.server.deps.DB.ExecContext(context.Background(),
		`UPDATE screens SET png_storage_key = NULL WHERE id = ?`, f.screen); err != nil {
		t.Fatalf("clear png_storage_key: %v", err)
	}
	claims := &auth.Claims{Sub: f.user, Tenants: []string{f.tenantA}}
	req := pngRequest("/v1/projects/"+f.slug+"/screens/"+f.screen+"/png", claims, f.slug, f.screen)
	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("null png_storage_key: got %d, want 404", rec.Code)
	}
}

func TestHandleScreenPNG_PathTraversalRejected(t *testing.T) {
	f := newPNGHandlerFixture(t)
	// Inject a malicious storage key — should never happen with server-generated
	// UUIDs, but the handler must defend against it. The path-traversal payload
	// resolves outside data/screens.
	if _, err := f.server.deps.DB.ExecContext(context.Background(),
		`UPDATE screens SET png_storage_key = '../../../etc/passwd' WHERE id = ?`, f.screen); err != nil {
		t.Fatalf("inject: %v", err)
	}
	claims := &auth.Claims{Sub: f.user, Tenants: []string{f.tenantA}}
	req := pngRequest("/v1/projects/"+f.slug+"/screens/"+f.screen+"/png", claims, f.slug, f.screen)
	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("traversal: got %d, want 400", rec.Code)
	}
}

func TestHandleScreenPNG_MissingPathParams_400(t *testing.T) {
	f := newPNGHandlerFixture(t)
	claims := &auth.Claims{Sub: f.user, Tenants: []string{f.tenantA}}
	req := requestWithClaims("GET", "/v1/projects//screens//png", nil, claims)
	// Path values intentionally not set — simulating misrouted request.
	rec := httptest.NewRecorder()
	f.server.HandleScreenPNG()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing params: got %d, want 400", rec.Code)
	}
}
