package projects

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	dbpkg "github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// overrideFixture wires the minimum dependencies for screen-overrides tests:
// fresh DB with migrations, two tenants, one user per tenant, one project +
// version + flow + screen for each tenant. Mirrors pngHandlerFixture's seeding
// approach.
type overrideFixture struct {
	server     *Server
	dbHandle   *dbpkg.DB
	tenantA    string
	tenantB    string
	userA      string
	userB      string
	slugA      string
	slugB      string
	versionA   string
	flowA      string
	screenA    string
	screenB    string // belongs to tenant B + slugB
}

func newOverrideFixture(t *testing.T) *overrideFixture {
	t.Helper()

	d, tA, tB, uA := newTestDB(t)
	// newTestDB only seeds tenants + userA. We need a userB for tenantB so
	// we can later seed a project for it (FK on owner_user_id).
	uB := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := d.CreateUser(context.Background(), dbpkg.User{
		ID: uB, Email: "tb@example.com", PasswordHash: "x", Role: "user",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create userB: %v", err)
	}

	srv := NewServer(ServerDeps{
		DB:          d,
		AuditLogger: &AuditLogger{DB: d},
		DataDir:     t.TempDir(),
	})

	// Seed tenant A: project + version + flow + screen.
	pA := uuid.NewString()
	vA := uuid.NewString()
	fA := uuid.NewString()
	sA := uuid.NewString()
	slugA := "ten-a-flow"
	for _, q := range seedRows(pA, vA, fA, sA, slugA, tA, uA, now) {
		if _, err := d.ExecContext(context.Background(), q.sql, q.args...); err != nil {
			t.Fatalf("seed tA: %v\nsql: %s", err, q.sql)
		}
	}

	// Seed tenant B: separate project + version + flow + screen.
	pB := uuid.NewString()
	vB := uuid.NewString()
	fB := uuid.NewString()
	sB := uuid.NewString()
	slugB := "ten-b-flow"
	for _, q := range seedRows(pB, vB, fB, sB, slugB, tB, uB, now) {
		if _, err := d.ExecContext(context.Background(), q.sql, q.args...); err != nil {
			t.Fatalf("seed tB: %v\nsql: %s", err, q.sql)
		}
	}

	return &overrideFixture{
		server:   srv,
		dbHandle: d,
		tenantA:  tA, tenantB: tB,
		userA: uA, userB: uB,
		slugA: slugA, slugB: slugB,
		versionA: vA, flowA: fA,
		screenA: sA, screenB: sB,
	}
}

type seedQuery struct {
	sql  string
	args []any
}

func seedRows(projectID, versionID, flowID, screenID, slug, tenantID, userID, now string) []seedQuery {
	return []seedQuery{
		{`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{projectID, slug, "Test", "mobile", "Plutus", "Path/Test", userID, tenantID, now, now}},
		{`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		  VALUES (?, ?, ?, 1, 'view_ready', ?, ?)`,
			[]any{versionID, projectID, tenantID, userID, now}},
		{`INSERT INTO flows (id, project_id, tenant_id, file_id, name, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{flowID, projectID, tenantID, "file-X", "Flow", now, now}},
		{`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, created_at)
		  VALUES (?, ?, ?, ?, 0, 0, 375, 812, ?, ?)`,
			[]any{screenID, versionID, flowID, tenantID, "logical-1", now}},
	}
}

// putOverride is a small helper to invoke HandlePutOverride with given path
// vars + body and a claims-bearing request.
func putOverride(t *testing.T, srv *Server, slug, screenID, figmaNodeID, tenantID, userID string, body putOverrideRequest) *httptest.ResponseRecorder {
	t.Helper()
	bs, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s", slug, screenID, figmaNodeID),
		bytes.NewReader(bs))
	r.SetPathValue("slug", slug)
	r.SetPathValue("id", screenID)
	r.SetPathValue("figma_node_id", figmaNodeID)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: userID, Tenants: []string{tenantID}}))
	w := httptest.NewRecorder()
	srv.HandlePutOverride(w, r)
	return w
}

func deleteOverride(t *testing.T, srv *Server, slug, screenID, figmaNodeID, tenantID, userID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s", slug, screenID, figmaNodeID),
		nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("id", screenID)
	r.SetPathValue("figma_node_id", figmaNodeID)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: userID, Tenants: []string{tenantID}}))
	w := httptest.NewRecorder()
	srv.HandleDeleteOverride(w, r)
	return w
}

func listScreenOverrides(t *testing.T, srv *Server, slug, screenID, tenantID, userID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides", slug, screenID), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("id", screenID)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: userID, Tenants: []string{tenantID}}))
	w := httptest.NewRecorder()
	srv.HandleListOverrides(w, r)
	return w
}

// ───────────────────────── Happy path (PUT new) ─────────────────────────

func TestHandlePutOverride_NewRow_Returns200_Revision1(t *testing.T) {
	fx := newOverrideFixture(t)
	w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-1", fx.tenantA, fx.userA,
		putOverrideRequest{
			Value:                "Hello",
			ExpectedRevision:     0,
			CanonicalPath:        "0/0/text",
			LastSeenOriginalText: "Original",
		})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Revision  int    `json:"revision"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Revision != 1 {
		t.Fatalf("expected revision=1; got %d", resp.Revision)
	}

	// Row exists in DB with the expected fields.
	var v string
	var rev int
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT value, revision FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "node-1",
	).Scan(&v, &rev); err != nil {
		t.Fatalf("expected row in DB: %v", err)
	}
	if v != "Hello" || rev != 1 {
		t.Fatalf("expected (Hello,1), got (%s,%d)", v, rev)
	}

	// Audit row carries flow_id in details.
	gotFlow := auditDetailString(t, fx.dbHandle, "override.text.set")
	if !strings.Contains(gotFlow, fx.flowA) {
		t.Fatalf("expected audit details to include flow_id %s; got %s", fx.flowA, gotFlow)
	}
}

// ───────────────────────── Happy path (PUT update) ──────────────────────

func TestHandlePutOverride_ExistingRow_RevisionIncrements(t *testing.T) {
	fx := newOverrideFixture(t)
	// First write — rev=1.
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-2", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "First", ExpectedRevision: 0, CanonicalPath: "p"}); w.Code != http.StatusOK {
		t.Fatalf("seed write: %d", w.Code)
	}

	// Second write — expected_revision=1 → rev=2.
	w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-2", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "Second", ExpectedRevision: 1, CanonicalPath: "p"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct{ Revision int `json:"revision"` }
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Revision != 2 {
		t.Fatalf("expected revision=2; got %d", resp.Revision)
	}

	// Old value not retained: SELECT must return "Second".
	var v string
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT value FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "node-2",
	).Scan(&v); err != nil || v != "Second" {
		t.Fatalf("expected value=Second got %q (err=%v)", v, err)
	}
}

// ───────────────────────── Edge: ExpectedRevision=0 on existing row ─────

func TestHandlePutOverride_ExpectedRev0_OnExistingRow_409(t *testing.T) {
	fx := newOverrideFixture(t)
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-3", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "x", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}

	w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-3", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "y", ExpectedRevision: 0})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Error           string `json:"error"`
		CurrentRevision int    `json:"current_revision"`
		CurrentValue    string `json:"current_value"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Error != "revision_conflict" {
		t.Fatalf("expected revision_conflict; got %q", resp.Error)
	}
	if resp.CurrentRevision != 1 {
		t.Fatalf("expected current_revision=1; got %d", resp.CurrentRevision)
	}
	if resp.CurrentValue != "x" {
		t.Fatalf("expected current_value=x; got %q", resp.CurrentValue)
	}
}

// ───────────────────────── Edge: Body > 16 KB ───────────────────────────

func TestHandlePutOverride_OversizeBody_413(t *testing.T) {
	fx := newOverrideFixture(t)
	// 16KB+1 byte of value content overflows MaxBytesReader at the JSON
	// envelope level too. We use raw bytes to exceed the cap deterministically.
	huge := strings.Repeat("x", MaxOverrideValueBytes+512)
	bs, _ := json.Marshal(putOverrideRequest{Value: huge, ExpectedRevision: 0})
	r := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s", fx.slugA, fx.screenA, "node-big"),
		bytes.NewReader(bs))
	r.SetPathValue("slug", fx.slugA)
	r.SetPathValue("id", fx.screenA)
	r.SetPathValue("figma_node_id", "node-big")
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: fx.userA, Tenants: []string{fx.tenantA}}))
	w := httptest.NewRecorder()
	fx.server.HandlePutOverride(w, r)

	if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 413 or 400; got %d body=%s", w.Code, w.Body.String())
	}

	// No row written.
	var n int
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "node-big",
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected no row; got %d", n)
	}
}

// ───────────────────────── Error: Stale expected_revision ───────────────

func TestHandlePutOverride_StaleRevision_409_NoWrite(t *testing.T) {
	fx := newOverrideFixture(t)
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-stale", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "v1", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-stale", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "v2", ExpectedRevision: 1}); w.Code != http.StatusOK {
		t.Fatalf("rev2: %d", w.Code)
	}

	// Stale write expects revision=1 but live row has revision=2.
	w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-stale", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "v3", ExpectedRevision: 1})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409; got %d body=%s", w.Code, w.Body.String())
	}

	// Value remains v2 (no write happened on the conflicting attempt).
	var v string
	var rev int
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT value, revision FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "node-stale",
	).Scan(&v, &rev); err != nil {
		t.Fatal(err)
	}
	if v != "v2" || rev != 2 {
		t.Fatalf("expected (v2,2) intact; got (%s,%d)", v, rev)
	}
}

// ───────────────────────── Error: Cross-tenant 404 ──────────────────────

func TestHandlePutOverride_CrossTenant_404(t *testing.T) {
	fx := newOverrideFixture(t)
	// User from tenant A targets tenant B's screen.
	w := putOverride(t, fx.server, fx.slugB, fx.screenB, "node-x", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "leak", ExpectedRevision: 0})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404; got %d body=%s", w.Code, w.Body.String())
	}
}

// ───────────────────────── DELETE: missing → 204 ────────────────────────

func TestHandleDeleteOverride_MissingRow_204(t *testing.T) {
	fx := newOverrideFixture(t)
	w := deleteOverride(t, fx.server, fx.slugA, fx.screenA, "no-such-node", fx.tenantA, fx.userA)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204; got %d body=%s", w.Code, w.Body.String())
	}
}

// ───────────────────────── DELETE: happy path emits audit ───────────────

func TestHandleDeleteOverride_Existing_RemovesRow_EmitsReset(t *testing.T) {
	fx := newOverrideFixture(t)
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-del", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "rm", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}

	w := deleteOverride(t, fx.server, fx.slugA, fx.screenA, "node-del", fx.tenantA, fx.userA)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204; got %d body=%s", w.Code, w.Body.String())
	}

	// Row gone.
	var n int
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "node-del",
	).Scan(&n)
	if n != 0 {
		t.Fatalf("expected row removed; still %d", n)
	}

	// override.text.reset audit row exists with flow_id in details.
	got := auditDetailString(t, fx.dbHandle, "override.text.reset")
	if !strings.Contains(got, fx.flowA) {
		t.Fatalf("expected reset audit details to carry flow_id %s; got %s", fx.flowA, got)
	}
}

// ───────────────────────── BULK: 100 rows in one tx, shared bulk_id ─────

func TestHandleBulkUpsertOverrides_100Rows_SingleTx_SharedBulkID(t *testing.T) {
	fx := newOverrideFixture(t)

	items := make([]bulkOverrideItem, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, bulkOverrideItem{
			ScreenID:      fx.screenA,
			FigmaNodeID:   fmt.Sprintf("bulk-%03d", i),
			Value:         "v",
			CanonicalPath: "p",
		})
	}
	bs, _ := json.Marshal(bulkUpsertOverridesRequest{Items: items})

	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/text-overrides/bulk", fx.slugA),
		bytes.NewReader(bs))
	r.SetPathValue("slug", fx.slugA)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: fx.userA, Tenants: []string{fx.tenantA}}))
	w := httptest.NewRecorder()
	fx.server.HandleBulkUpsertOverrides(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		BulkID  string   `json:"bulk_id"`
		Updated []string `json:"updated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.BulkID == "" || len(resp.Updated) != 100 {
		t.Fatalf("expected 100 updated + bulk_id; got %d updates, bulk_id=%q",
			len(resp.Updated), resp.BulkID)
	}

	// 100 audit rows, all carrying the same bulk_id.
	rows, err := fx.dbHandle.QueryContext(context.Background(),
		`SELECT details FROM audit_log WHERE event_type = 'override.text.set'
		   AND tenant_id = ?`, fx.tenantA)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var detail string
		_ = rows.Scan(&detail)
		if !strings.Contains(detail, resp.BulkID) {
			t.Fatalf("audit row missing bulk_id: %s", detail)
		}
		count++
	}
	if count != 100 {
		t.Fatalf("expected 100 audit rows; got %d", count)
	}

	// Confirm all 100 override rows persisted.
	var n int
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM screen_text_overrides WHERE screen_id = ?`,
		fx.screenA,
	).Scan(&n)
	if n != 100 {
		t.Fatalf("expected 100 override rows; got %d", n)
	}
}

// ───────────────────────── BULK: 101 rows → 400 invalid_payload ─────────

func TestHandleBulkUpsertOverrides_101Rows_400(t *testing.T) {
	fx := newOverrideFixture(t)

	items := make([]bulkOverrideItem, 0, 101)
	for i := 0; i < 101; i++ {
		items = append(items, bulkOverrideItem{
			ScreenID:    fx.screenA,
			FigmaNodeID: fmt.Sprintf("over-%03d", i),
			Value:       "v",
		})
	}
	bs, _ := json.Marshal(bulkUpsertOverridesRequest{Items: items})

	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/text-overrides/bulk", fx.slugA),
		bytes.NewReader(bs))
	r.SetPathValue("slug", fx.slugA)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: fx.userA, Tenants: []string{fx.tenantA}}))
	w := httptest.NewRecorder()
	fx.server.HandleBulkUpsertOverrides(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_payload") {
		t.Fatalf("expected invalid_payload code; got %s", w.Body.String())
	}
}

// ───────────────────────── Integration: GET returns active + orphaned ──

func TestHandleListOverrides_Mixed_ActiveAndOrphaned(t *testing.T) {
	fx := newOverrideFixture(t)

	// Two override rows: one we leave active, one we mark orphaned via the
	// repo helper (simulates U3's re-attach failure).
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "act-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "active", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed active: %d", w.Code)
	}
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "orph-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "soon-orphaned", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed orphaned: %d", w.Code)
	}
	var orphID string
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT id FROM screen_text_overrides WHERE figma_node_id = ?`, "orph-1",
	).Scan(&orphID); err != nil {
		t.Fatalf("lookup orph id: %v", err)
	}
	repo := NewTenantRepo(fx.dbHandle.DB, fx.tenantA)
	if err := repo.MarkOverridesOrphaned(context.Background(), []string{orphID}); err != nil {
		t.Fatalf("mark orphaned: %v", err)
	}

	w := listScreenOverrides(t, fx.server, fx.slugA, fx.screenA, fx.tenantA, fx.userA)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Overrides []ScreenTextOverride `json:"overrides"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	statuses := map[string]string{}
	for _, o := range resp.Overrides {
		statuses[o.FigmaNodeID] = o.Status
	}
	if statuses["act-1"] != "active" {
		t.Fatalf("expected act-1 active; got %q", statuses["act-1"])
	}
	if statuses["orph-1"] != "orphaned" {
		t.Fatalf("expected orph-1 orphaned; got %q", statuses["orph-1"])
	}
}

// ───────────────────────── Repo: BulkUpsert single tx integrity ─────────

func TestRepo_BulkUpsertOverrides_AuditFailure_RollsBack(t *testing.T) {
	fx := newOverrideFixture(t)
	repo := NewTenantRepo(fx.dbHandle.DB, fx.tenantA)

	// Force an audit failure on the second row by returning an error.
	rows := []*BulkOverrideRow{
		{ScreenID: fx.screenA, FigmaNodeID: "ok-1", Value: "a", PerRowAudit: noopAudit},
		{ScreenID: fx.screenA, FigmaNodeID: "fail-2", Value: "b",
			PerRowAudit: func(tx *sql.Tx, flowID string, newRev int) error {
				return fmt.Errorf("synthetic audit failure")
			}},
	}

	if _, err := repo.BulkUpsertOverrides(context.Background(), fx.slugA, rows, fx.userA); err == nil {
		t.Fatalf("expected error from synthetic audit failure")
	}

	// Neither row should be persisted (whole tx rolled back).
	var n int
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM screen_text_overrides WHERE screen_id = ?`, fx.screenA,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows after rollback; got %d", n)
	}
}

func noopAudit(tx *sql.Tx, flowID string, newRev int) error { return nil }

// ───────────────────────── helpers ──────────────────────────────────────

// auditDetailString returns the most recent audit_log.details for the given
// event_type (across all rows, all tenants — fixture has only one). t fails
// if no row exists.
func auditDetailString(t *testing.T, d *dbpkg.DB, eventType string) string {
	t.Helper()
	var details string
	if err := d.QueryRowContext(context.Background(),
		`SELECT details FROM audit_log WHERE event_type = ? ORDER BY ts DESC LIMIT 1`,
		eventType,
	).Scan(&details); err != nil {
		t.Fatalf("expected at least one %s audit row; got err=%v", eventType, err)
	}
	return details
}
