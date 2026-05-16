package projects

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// server_figma_autosync_test.go — #15 + #16 audit fix coverage.
//
// Pins the HTTP-level contract for the autosync admin endpoints:
//   - HandleFigmaAutosyncClearQuarantine (DELETE quarantine)
//   - HandleFigmaAutosyncListState (GET state)
// plus the per-tenant SQLite advisory lease primitives that replace
// the old process-local sync.Mutex (audit fix #10).

func newAutosyncServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	d, tA, _, uA := newTestDB(t)
	srv := NewServer(ServerDeps{DB: d, DataDir: t.TempDir()})
	return srv, tA, uA
}

func adminClaims(tenantID, userID string) *auth.Claims {
	return &auth.Claims{
		Sub:     userID,
		IsAdmin: true,
		Role:    auth.RoleSuperAdmin,
		Tenants: []string{tenantID},
	}
}

func nonAdminClaims(tenantID, userID string) *auth.Claims {
	return &auth.Claims{Sub: userID, Role: "user", Tenants: []string{tenantID}}
}

// TestClearQuarantine_RequiresAdmin — non-admin claims get 403.
func TestClearQuarantine_RequiresAdmin(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	r := httptest.NewRequest(http.MethodDelete,
		"/v1/admin/figma-autosync/state/fk/p/s/quarantine", nil)
	r.SetPathValue("file_key", "fk")
	r.SetPathValue("page_id", "p")
	r.SetPathValue("section_id", "s")
	r = r.WithContext(WithClaims(context.Background(), nonAdminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncClearQuarantine(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status: got %d want 403", w.Code)
	}
}

// TestClearQuarantine_405OnGET — handler only honors DELETE.
func TestClearQuarantine_405OnGET(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	r := httptest.NewRequest(http.MethodGet,
		"/v1/admin/figma-autosync/state/fk/p/s/quarantine", nil)
	r = r.WithContext(WithClaims(context.Background(), adminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncClearQuarantine(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d want 405", w.Code)
	}
}

// TestClearQuarantine_MissingPathParams_400 — missing file_key etc.
// returns 400 rather than rolling on to the DB.
func TestClearQuarantine_MissingPathParams_400(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	r := httptest.NewRequest(http.MethodDelete,
		"/v1/admin/figma-autosync/state///quarantine", nil)
	r = r.WithContext(WithClaims(context.Background(), adminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncClearQuarantine(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", w.Code)
	}
}

// TestClearQuarantine_QuarantinedRow_200AndAudit — happy path: a
// quarantined row is reset, response is 200, audit_log row written.
func TestClearQuarantine_QuarantinedRow_200AndAudit(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tenantID)
	ctx := context.Background()

	// Drive to quarantined via 5 consecutive error upserts.
	const fk, pg, sec = "fk", "p", "s"
	for i := 0; i < AutoSyncMaxRetries; i++ {
		if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
			FileKey: fk, PageID: pg, SectionID: sec,
			LastAttemptStatus: "error", ErrorMessage: "Figma 500",
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, _ := repo.LookupAutoSyncState(ctx, fk, pg, sec)
	if got.LastAttemptStatus != "quarantined" {
		t.Fatalf("setup: row not quarantined (status=%q)", got.LastAttemptStatus)
	}

	r := httptest.NewRequest(http.MethodDelete,
		"/v1/admin/figma-autosync/state/"+fk+"/"+pg+"/"+sec+"/quarantine", nil)
	r.SetPathValue("file_key", fk)
	r.SetPathValue("page_id", pg)
	r.SetPathValue("section_id", sec)
	r = r.WithContext(WithClaims(ctx, adminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncClearQuarantine(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", w.Code, w.Body.String())
	}
	var body struct {
		OK      bool `json:"ok"`
		Cleared bool `json:"cleared"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.OK || !body.Cleared {
		t.Errorf("response: %+v", body)
	}

	// Audit log row landed.
	var auditCount int
	if err := srv.deps.DB.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE event_type = ? AND tenant_id = ?`,
		"figma_autosync_clear_quarantine", tenantID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit rows: got %d want 1", auditCount)
	}
}

// TestClearQuarantine_HealthyRow_404 — #11 audit fix: clearing a row
// that is not in 'quarantined' state must 404 and leave the row
// untouched. Prior implementation silently wiped state.
func TestClearQuarantine_HealthyRow_404(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tenantID)
	ctx := context.Background()
	const fk, pg, sec = "fk-ok", "p", "s"
	if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
		FileKey: fk, PageID: pg, SectionID: sec,
		ContentHash: "h-prev", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete,
		"/v1/admin/figma-autosync/state/"+fk+"/"+pg+"/"+sec+"/quarantine", nil)
	r.SetPathValue("file_key", fk)
	r.SetPathValue("page_id", pg)
	r.SetPathValue("section_id", sec)
	r = r.WithContext(WithClaims(ctx, adminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncClearQuarantine(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", w.Code)
	}
	// Row intact.
	after, _ := repo.LookupAutoSyncState(ctx, fk, pg, sec)
	if after.LastAttemptStatus != "ok" {
		t.Errorf("status corrupted: got %q want ok", after.LastAttemptStatus)
	}
	if after.ContentHash != "h-prev" {
		t.Errorf("content_hash wiped: got %q want h-prev", after.ContentHash)
	}
}

// TestClearQuarantine_NoSuchRow_404 — DELETE on a section that has no
// state row at all returns 404.
func TestClearQuarantine_NoSuchRow_404(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	r := httptest.NewRequest(http.MethodDelete,
		"/v1/admin/figma-autosync/state/none/p/s/quarantine", nil)
	r.SetPathValue("file_key", "none")
	r.SetPathValue("page_id", "p")
	r.SetPathValue("section_id", "s")
	r = r.WithContext(WithClaims(context.Background(), adminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncClearQuarantine(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", w.Code)
	}
}

// TestListState_RequiresAdmin — #12 audit fix endpoint must enforce
// admin role.
func TestListState_RequiresAdmin(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/figma-autosync/state", nil)
	r = r.WithContext(WithClaims(context.Background(), nonAdminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncListState(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status: got %d want 403", w.Code)
	}
}

// TestListState_PaginatesAndFilters — verifies the filter + pagination
// envelope works as documented.
func TestListState_PaginatesAndFilters(t *testing.T) {
	srv, tenantID, userID := newAutosyncServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tenantID)
	ctx := context.Background()

	// Seed 3 rows: 2 'error', 1 'ok'.
	for i, status := range []string{"error", "error", "ok"} {
		key := []string{"fk-a", "fk-a", "fk-b"}[i]
		if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
			FileKey:           key,
			PageID:            "p",
			SectionID:         "s" + string(rune('0'+i)),
			LastAttemptStatus: status,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/admin/figma-autosync/state?status=error", nil)
	r = r.WithContext(WithClaims(ctx, adminClaims(tenantID, userID)))
	w := httptest.NewRecorder()
	srv.HandleFigmaAutosyncListState(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Rows  []AutoSyncState `json:"rows"`
		Count int             `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 2 {
		t.Errorf("count: got %d want 2 (status=error filter)", body.Count)
	}
}

// ─── #16 audit fix: per-tenant SQLite advisory lease ──────────────────────────

// TestAutosyncLease_AcquireReleaseRoundtrip — successful acquire +
// release cycle.
func TestAutosyncLease_AcquireReleaseRoundtrip(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	ctx := context.Background()
	const holder = "host-A:42:abc"

	ok, err := TryAcquireAutosyncLease(ctx, d.DB, tA, holder, 1*1e9 /* 1s */)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !ok {
		t.Fatal("first acquire returned false")
	}
	if err := ReleaseAutosyncLease(ctx, d.DB, tA, holder); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestAutosyncLease_DoubleAcquireFails — a second acquire while the
// first holder still owns the lease returns false (the F11/multi-
// replica safety property).
func TestAutosyncLease_DoubleAcquireFails(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	ctx := context.Background()
	ok, err := TryAcquireAutosyncLease(ctx, d.DB, tA, "host-A:1:x", 1*1e9)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	ok2, err2 := TryAcquireAutosyncLease(ctx, d.DB, tA, "host-B:2:y", 1*1e9)
	if err2 != nil {
		t.Fatalf("second acquire err: %v", err2)
	}
	if ok2 {
		t.Error("second acquire returned true while first holder owns lease")
	}
}

// TestAutosyncLease_ExpiredLeaseReclaimed — when the existing lease's
// expires_at is in the past, a new acquirer wins.
func TestAutosyncLease_ExpiredLeaseReclaimed(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	ctx := context.Background()

	// Insert an expired row directly (negative TTL would round-trip
	// through the function but is clearer this way).
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO figma_autosync_lease (tenant_id, holder_id, acquired_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		tA, "dead-holder", "1970-01-01T00:00:00Z", "1970-01-01T00:00:01Z",
	); err != nil {
		t.Fatalf("seed expired: %v", err)
	}

	ok, err := TryAcquireAutosyncLease(ctx, d.DB, tA, "fresh-holder", 1*1e9)
	if err != nil {
		t.Fatalf("acquire over expired: %v", err)
	}
	if !ok {
		t.Error("acquire over expired lease returned false")
	}
}

// TestAutosyncLease_PerTenantIndependence — leases are scoped per
// tenant_id; holding A's lease never blocks B's.
func TestAutosyncLease_PerTenantIndependence(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	ctx := context.Background()
	if ok, _ := TryAcquireAutosyncLease(ctx, d.DB, tA, "h", 1*1e9); !ok {
		t.Fatal("A acquire failed")
	}
	if ok, _ := TryAcquireAutosyncLease(ctx, d.DB, tB, "h", 1*1e9); !ok {
		t.Error("B acquire blocked by A's lease")
	}
}

// TestAutosyncLease_ReleaseScopedToHolder — DELETE is keyed on
// holder_id so replica B can't accidentally free A's lease after its
// own expired and got reclaimed.
func TestAutosyncLease_ReleaseScopedToHolder(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	ctx := context.Background()
	if ok, _ := TryAcquireAutosyncLease(ctx, d.DB, tA, "A", 1*1e9); !ok {
		t.Fatal("A acquire failed")
	}
	// B tries to release — should be a no-op, A still holds it.
	if err := ReleaseAutosyncLease(ctx, d.DB, tA, "B"); err != nil {
		t.Fatalf("B release: %v", err)
	}
	// A still can't double-acquire while it owns an unexpired one.
	if ok, _ := TryAcquireAutosyncLease(ctx, d.DB, tA, "A2", 1*1e9); ok {
		t.Error("A2 acquired while A still held the lease (release_scope leak)")
	}
}
