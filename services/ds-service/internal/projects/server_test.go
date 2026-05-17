package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// newTestServer wires a Server with stubs and a fresh DB seeded with one
// tenant + user. Returns the server, the tenant ID, the user ID, the broker,
// and the audit-job notifications channel for assertion access.
func newTestServer(t *testing.T) (*Server, string, string, *stubBroker, *AuditEnqueuer) {
	t.Helper()
	d, tA, _, uA := newTestDB(t)

	broker := &stubBroker{}
	rateLimiter := NewRateLimiter()
	t.Cleanup(rateLimiter.Close)
	idempotency := NewIdempotencyCache()
	t.Cleanup(idempotency.Close)
	tickets := sse.NewMemoryTicketStore(0)
	t.Cleanup(tickets.Close)
	enqueuer := NewAuditEnqueuer()

	broker2 := sse.NewMemoryBroker(sse.BrokerOptions{})
	t.Cleanup(broker2.Close)
	deps := ServerDeps{
		DB:            d,
		Broker:        broker2,
		Tickets:       tickets,
		RateLimiter:   rateLimiter,
		Idempotency:   idempotency,
		AuditLogger:   &AuditLogger{DB: d},
		AuditEnqueuer: enqueuer,
		DataDir:       t.TempDir(),
		// PipelineFactory: nil — we want the export handler to short-circuit
		// after writing the skeleton so tests don't hit Figma. Setting it
		// to nil makes the goroutine spawn no-op.
		PipelineFactory: nil,
		Log:             nil,
	}
	srv := NewServer(deps)
	return srv, tA, uA, broker, enqueuer
}

// validExportBody returns a payload that passes validateExport.
func validExportBody(t *testing.T) []byte {
	t.Helper()
	body := ExportRequest{
		IdempotencyKey: uuid.NewString(),
		FileID:         "FILE-K",
		FileName:       "Test File",
		Flows: []FlowPayload{{
			Platform:    "mobile",
			Product:     "Plutus",
			Path:        "Onboarding",
			Name:        "FlowA",
			PersonaName: "Trader",
			Frames: []FramePayload{
				{FrameID: "fig-1", X: 0, Y: 0, Width: 375, Height: 812,
					VariableCollectionID: "VC", ModeID: "light", ModeLabel: "light"},
				{FrameID: "fig-2", X: 0, Y: 1500, Width: 375, Height: 812,
					VariableCollectionID: "VC", ModeID: "dark", ModeLabel: "dark"},
			},
		}},
	}
	bs, _ := json.Marshal(body)
	return bs
}

// requestWithClaims wraps an http.Request with claims attached.
func requestWithClaims(method, target string, body []byte, claims *auth.Claims) *http.Request {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	r = r.WithContext(WithClaims(context.Background(), claims))
	return r
}

func TestHandleExport_Success(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", validExportBody(t), claims)
	w := httptest.NewRecorder()

	srv.HandleExport(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var resp ExportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ProjectID == "" || resp.VersionID == "" || resp.TraceID == "" {
		t.Fatalf("empty response field: %+v", resp)
	}
	if resp.SchemaVersion != ProjectsSchemaVersion {
		t.Fatalf("schema_version mismatch")
	}
}

func TestHandleExport_IdempotencyReplayReturns409Cached(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}
	body := validExportBody(t)

	r1 := requestWithClaims(http.MethodPost, "/v1/projects/export", body, claims)
	w1 := httptest.NewRecorder()
	srv.HandleExport(w1, r1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call: expected 202, got %d", w1.Code)
	}

	r2 := requestWithClaims(http.MethodPost, "/v1/projects/export", body, claims)
	w2 := httptest.NewRecorder()
	srv.HandleExport(w2, r2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("replay: expected 409, got %d", w2.Code)
	}
	// Response body should match the cached first response.
	if !bytes.Equal(w1.Body.Bytes(), w2.Body.Bytes()) {
		t.Fatalf("cached body should match original")
	}
}

func TestHandleExport_RejectsCRLFInPath(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}

	body := ExportRequest{
		IdempotencyKey: uuid.NewString(),
		FileID:         "FILE-K",
		Flows: []FlowPayload{{
			Platform: "mobile", Product: "Plutus",
			Path:    "Onboarding\r\nbad",
			Name:    "Flow",
			Frames:  []FramePayload{{FrameID: "f1", Width: 100, Height: 100}},
		}},
	}
	bs, _ := json.Marshal(body)
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", bs, claims)
	w := httptest.NewRecorder()

	srv.HandleExport(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on CRLF; got %d", w.Code)
	}
}

func TestHandleExport_RejectsExcessFlows(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}

	flows := make([]FlowPayload, MaxFlowsPerExport+1)
	for i := range flows {
		flows[i] = FlowPayload{
			Platform: "mobile", Product: "P", Path: "X", Name: "F",
			Frames: []FramePayload{{FrameID: "f", Width: 1, Height: 1}},
		}
	}
	body := ExportRequest{IdempotencyKey: uuid.NewString(), FileID: "F", Flows: flows}
	bs, _ := json.Marshal(body)
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", bs, claims)
	w := httptest.NewRecorder()

	srv.HandleExport(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on excess flows; got %d", w.Code)
	}
}

func TestHandleExport_RejectsExcessFramesPerFlow(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}

	frames := make([]FramePayload, MaxFramesPerFlow+1)
	for i := range frames {
		frames[i] = FramePayload{FrameID: "f" + uuid.NewString(), Width: 1, Height: 1}
	}
	body := ExportRequest{
		IdempotencyKey: uuid.NewString(), FileID: "F",
		Flows: []FlowPayload{{
			Platform: "mobile", Product: "P", Path: "X", Name: "F", Frames: frames,
		}},
	}
	bs, _ := json.Marshal(body)
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", bs, claims)
	w := httptest.NewRecorder()

	srv.HandleExport(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on excess frames; got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "max") {
		t.Fatalf("expected explicit limit message; got %s", w.Body.String())
	}
}

func TestHandleExport_RateLimitFires(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}

	// Exhaust the per-user bucket.
	for i := 0; i < UserBucketSize; i++ {
		body := validExportBody(t)
		r := requestWithClaims(http.MethodPost, "/v1/projects/export", body, claims)
		w := httptest.NewRecorder()
		srv.HandleExport(w, r)
		if w.Code != http.StatusAccepted {
			t.Fatalf("call %d: expected 202, got %d", i, w.Code)
		}
	}
	// Next one should rate-limit.
	body := validExportBody(t)
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", body, claims)
	w := httptest.NewRecorder()
	srv.HandleExport(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429; got %d", w.Code)
	}
}

func TestHandleExport_UnauthenticatedRejected(t *testing.T) {
	srv, _, _, _, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/projects/export", bytes.NewReader(validExportBody(t)))
	w := httptest.NewRecorder()
	srv.HandleExport(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", w.Code)
	}
}

func TestHandleExport_PayloadTenantIDIgnored(t *testing.T) {
	// JWT carries tenantA; payload (if it tried to override) is ignored.
	// Our payload schema doesn't actually accept tenant_id so this is a
	// schema-level guarantee — verify the handler resolves tenant from claims
	// only.
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", validExportBody(t), claims)
	w := httptest.NewRecorder()
	srv.HandleExport(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202; got %d body=%s", w.Code, w.Body.String())
	}
	var resp ExportResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Verify the row really lives in tenantA.
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	if _, err := repo.GetVersion(context.Background(), resp.VersionID); err != nil {
		t.Fatalf("version not visible to original tenant: %v", err)
	}
}

func TestHandleExport_NoTenantInClaims_403(t *testing.T) {
	srv, _, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: nil} // no tenant
	r := requestWithClaims(http.MethodPost, "/v1/projects/export", validExportBody(t), claims)
	w := httptest.NewRecorder()
	srv.HandleExport(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403; got %d", w.Code)
	}
}

// TestHandleProjectGet_BundledResponse covers U12 — the GET response must
// carry versions / screens / screen_modes / available_personas alongside
// the project so the frontend can hydrate atlas + tabs in one round-trip.
func TestHandleProjectGet_BundledResponse(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	ctx := context.Background()

	p, err := repo.UpsertProject(ctx, Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	v1, err := repo.CreateVersion(ctx, p.ID, uA)
	if err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	v2, err := repo.CreateVersion(ctx, p.ID, uA)
	if err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	f, err := repo.UpsertFlow(ctx, Flow{ProjectID: p.ID, FileID: "F", Name: "Flow"})
	if err != nil {
		t.Fatalf("seed flow: %v", err)
	}
	// Two screens on the latest version (v2), one on v1 — verifies the
	// handler resolves "latest by version_index" rather than dumping all.
	v2Screens := []Screen{
		{VersionID: v2.ID, FlowID: f.ID, X: 0, Y: 0, Width: 375, Height: 812},
		{VersionID: v2.ID, FlowID: f.ID, X: 0, Y: 1000, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(ctx, v2Screens); err != nil {
		t.Fatalf("seed v2 screens: %v", err)
	}
	v1Screens := []Screen{
		{VersionID: v1.ID, FlowID: f.ID, X: 0, Y: 0, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(ctx, v1Screens); err != nil {
		t.Fatalf("seed v1 screens: %v", err)
	}
	// One screen_mode on a v2 screen so the modes array is non-empty.
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := repo.InsertScreenModes(ctx, tx, []ScreenMode{
		{ScreenID: v2Screens[0].ID, ModeLabel: "light", FigmaFrameID: "fig-1"},
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert screen_mode: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// One persona so available_personas is non-empty.
	if _, err := repo.UpsertPersona(ctx, "Trader", uA); err != nil {
		t.Fatalf("seed persona: %v", err)
	}

	// Happy path: latest version implicit.
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}
	r := requestWithClaims(http.MethodGet, "/v1/projects/"+p.Slug, nil, claims)
	r.SetPathValue("slug", p.Slug)
	w := httptest.NewRecorder()
	srv.HandleProjectGet(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Project           map[string]any   `json:"project"`
		Versions          []map[string]any `json:"versions"`
		Screens           []map[string]any `json:"screens"`
		ScreenModes       []map[string]any `json:"screen_modes"`
		AvailablePersonas []map[string]any `json:"available_personas"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.Project == nil {
		t.Fatal("project missing from response")
	}
	if len(resp.Versions) != 2 {
		t.Fatalf("expected 2 versions; got %d", len(resp.Versions))
	}
	// Latest first — v2 should lead.
	if resp.Versions[0]["ID"] != v2.ID {
		t.Fatalf("expected versions ordered DESC; got first=%v", resp.Versions[0]["ID"])
	}
	if len(resp.Screens) != 2 {
		t.Fatalf("expected 2 screens (latest version only); got %d", len(resp.Screens))
	}
	if len(resp.ScreenModes) != 1 {
		t.Fatalf("expected 1 screen_mode; got %d", len(resp.ScreenModes))
	}
	if len(resp.AvailablePersonas) != 1 {
		t.Fatalf("expected 1 persona; got %d", len(resp.AvailablePersonas))
	}

	// `?v=<id>` pins the version → screens for v1 only (1 screen, 0 modes).
	r2 := requestWithClaims(http.MethodGet, "/v1/projects/"+p.Slug+"?v="+v1.ID, nil, claims)
	r2.SetPathValue("slug", p.Slug)
	w2 := httptest.NewRecorder()
	srv.HandleProjectGet(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("v= request expected 200; got %d", w2.Code)
	}
	var resp2 struct {
		Versions    []map[string]any `json:"versions"`
		Screens     []map[string]any `json:"screens"`
		ScreenModes []map[string]any `json:"screen_modes"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if len(resp2.Screens) != 1 {
		t.Fatalf("?v=v1: expected 1 screen; got %d", len(resp2.Screens))
	}
	if len(resp2.ScreenModes) != 0 {
		t.Fatalf("?v=v1: expected 0 screen_modes; got %d", len(resp2.ScreenModes))
	}
}

// TestHandleProjectGet_EmptyProject covers the just-created edge case —
// no versions, no screens, no modes. Arrays must still be present so the
// client renders an empty state rather than crashing on undefined.
func TestHandleProjectGet_EmptyProject(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	p, err := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}
	r := requestWithClaims(http.MethodGet, "/v1/projects/"+p.Slug, nil, claims)
	r.SetPathValue("slug", p.Slug)
	w := httptest.NewRecorder()
	srv.HandleProjectGet(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}

	// Decode as raw map so we can assert keys exist (vs. omitempty stripping).
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"project", "versions", "screens", "screen_modes", "available_personas"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("response missing key %q; body=%s", key, w.Body.String())
		}
	}
	// Versions/screens must be `[]`, not `null`.
	if string(resp["versions"]) != "[]" {
		t.Errorf("expected versions=[]; got %s", string(resp["versions"]))
	}
	if string(resp["screens"]) != "[]" {
		t.Errorf("expected screens=[]; got %s", string(resp["screens"]))
	}
	if string(resp["screen_modes"]) != "[]" {
		t.Errorf("expected screen_modes=[]; got %s", string(resp["screen_modes"]))
	}
}

func TestHandleProjectGet_CrossTenant_404(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	p, err := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Attacker request: same DB, but JWT carries a different tenant.
	otherTenant := uuid.NewString()
	claims := &auth.Claims{Sub: uA, Tenants: []string{otherTenant}}
	r := requestWithClaims(http.MethodGet, "/v1/projects/"+p.Slug, nil, claims)
	r.SetPathValue("slug", p.Slug)
	w := httptest.NewRecorder()
	srv.HandleProjectGet(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (no existence oracle); got %d", w.Code)
	}
}

func TestHandleEventsTicket_RoundTrip(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	p, _ := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}}

	r := requestWithClaims(http.MethodPost, "/v1/projects/"+p.Slug+"/events/ticket", nil, claims)
	r.SetPathValue("slug", p.Slug)
	r.Header.Set("X-Trace-ID", "trace-X")
	w := httptest.NewRecorder()
	srv.HandleEventsTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Ticket  string `json:"ticket"`
		TraceID string `json:"trace_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Ticket == "" {
		t.Fatal("expected ticket in response")
	}
	if resp.TraceID != "trace-X" {
		t.Fatalf("trace mismatch: %s", resp.TraceID)
	}

	// Redeem the ticket — should return our user/tenant/trace once, then fail.
	uid, tid, trace, ok := srv.deps.Tickets.RedeemTicket(resp.Ticket)
	if !ok || uid != uA || tid != tA || trace != "trace-X" {
		t.Fatalf("redeem mismatch: %s/%s/%s ok=%v", uid, tid, trace, ok)
	}
	if _, _, _, ok := srv.deps.Tickets.RedeemTicket(resp.Ticket); ok {
		t.Fatal("expected single-use ticket to fail second redeem")
	}
}

func TestHandleProjectEvents_RejectsTokenInQuery(t *testing.T) {
	srv, _, _, _, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/projects/some/events?token=jwt", nil)
	r.SetPathValue("slug", "some")
	w := httptest.NewRecorder()
	srv.HandleProjectEvents(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on JWT in query; got %d", w.Code)
	}
}

// ─── DRD ticket endpoints (U3 follow-up) ──────────────────────────────────

// seedDRDFlowForServer prepares a project + flow inside the server's DB so
// HandleDRDTicket / HandleSubFlowDRDTicket tests can refer to real
// (slug, flow_id) and (sub_product_slug, sub_flow_slug) tuples.
func seedDRDFlowForServer(t *testing.T, srv *Server, tenantID, userID string) (slug, flowID string) {
	t.Helper()
	repo := NewTenantRepo(srv.deps.DB.DB, tenantID)
	versionID, _ := seedFlowAndScreens(t, repo, userID)
	var fid string
	if err := srv.deps.DB.DB.QueryRow(
		`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID,
	).Scan(&fid); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	var s string
	if err := srv.deps.DB.DB.QueryRow(
		`SELECT p.slug FROM flows f JOIN projects p ON p.id = f.project_id WHERE f.id = ?`,
		fid,
	).Scan(&s); err != nil {
		t.Fatalf("project slug: %v", err)
	}
	return s, fid
}

// TestHandleDRDTicket_Characterization locks in the current contract for
// the legacy flow_id-keyed endpoint before the U3 refactor extracts
// issueDRDTicket. The post-refactor handler MUST produce the same JSON
// shape and the same single-use / cross-tenant behaviour.
//
// Recorded contract:
//   - 200 with { ticket, trace_id="drd:<flow_id>", flow_id, tenant_id,
//     user_id, role, expires_in=60 } on the happy path.
//   - 404 when (slug, flow_id) doesn't resolve under the tenant.
//   - 403 when claims have no tenant.
//   - Tickets are single-use: redeem succeeds once, then fails.
func TestHandleDRDTicket_Characterization(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	slug, flowID := seedDRDFlowForServer(t, srv, tA, uA)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}

	// 1. Happy path.
	r := requestWithClaims(http.MethodPost,
		"/v1/projects/"+slug+"/flows/"+flowID+"/drd/ticket", nil, claims)
	r.SetPathValue("slug", slug)
	r.SetPathValue("flow_id", flowID)
	w := httptest.NewRecorder()
	srv.HandleDRDTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("happy path expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Ticket    string `json:"ticket"`
		TraceID   string `json:"trace_id"`
		FlowID    string `json:"flow_id"`
		TenantID  string `json:"tenant_id"`
		UserID    string `json:"user_id"`
		Role      string `json:"role"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Ticket == "" {
		t.Error("expected non-empty ticket")
	}
	if want := "drd:" + flowID; resp.TraceID != want {
		t.Errorf("trace_id=%q want %q", resp.TraceID, want)
	}
	if resp.FlowID != flowID {
		t.Errorf("flow_id=%q want %q", resp.FlowID, flowID)
	}
	if resp.TenantID != tA {
		t.Errorf("tenant_id=%q want %q", resp.TenantID, tA)
	}
	if resp.UserID != uA {
		t.Errorf("user_id=%q want %q", resp.UserID, uA)
	}
	if resp.Role != "editor" {
		t.Errorf("role=%q want editor", resp.Role)
	}
	if resp.ExpiresIn != 60 {
		t.Errorf("expires_in=%d want 60", resp.ExpiresIn)
	}

	// 2. Single-use: redeem once succeeds, second fails.
	uid, tid, traceID, ok := srv.deps.Tickets.RedeemTicket(resp.Ticket)
	if !ok || uid != uA || tid != tA || traceID != "drd:"+flowID {
		t.Fatalf("first redeem mismatch: %s/%s/%s ok=%v", uid, tid, traceID, ok)
	}
	if _, _, _, ok := srv.deps.Tickets.RedeemTicket(resp.Ticket); ok {
		t.Fatal("expected single-use ticket to fail second redeem")
	}

	// 3. Unknown flow_id → 404.
	r2 := requestWithClaims(http.MethodPost,
		"/v1/projects/"+slug+"/flows/nope/drd/ticket", nil, claims)
	r2.SetPathValue("slug", slug)
	r2.SetPathValue("flow_id", "nope")
	w2 := httptest.NewRecorder()
	srv.HandleDRDTicket(w2, r2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("unknown flow_id: expected 404, got %d", w2.Code)
	}

	// 4. Cross-tenant — different tenant in claims, same flow_id → 404
	//    (no existence oracle).
	otherTenant := uuid.NewString()
	otherClaims := &auth.Claims{Sub: uA, Tenants: []string{otherTenant}, Role: "editor"}
	r3 := requestWithClaims(http.MethodPost,
		"/v1/projects/"+slug+"/flows/"+flowID+"/drd/ticket", nil, otherClaims)
	r3.SetPathValue("slug", slug)
	r3.SetPathValue("flow_id", flowID)
	w3 := httptest.NewRecorder()
	srv.HandleDRDTicket(w3, r3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("cross-tenant: expected 404, got %d body=%s", w3.Code, w3.Body.String())
	}

	// 5. No claims at all → 401.
	r4 := httptest.NewRequest(http.MethodPost,
		"/v1/projects/"+slug+"/flows/"+flowID+"/drd/ticket", nil)
	r4.SetPathValue("slug", slug)
	r4.SetPathValue("flow_id", flowID)
	w4 := httptest.NewRecorder()
	srv.HandleDRDTicket(w4, r4)
	if w4.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d", w4.Code)
	}
}

// seedSubFlowForServer creates a sub_product + sub_flow on the server's DB.
func seedSubFlowForServer(t *testing.T, srv *Server, tenantID, productName, flowName string) (subProductSlug, subFlowSlug, subFlowID string) {
	t.Helper()
	repo := NewTenantRepo(srv.deps.DB.DB, tenantID)
	ctx := context.Background()
	sp, err := repo.UpsertSubProduct(ctx, productName)
	if err != nil {
		t.Fatalf("upsert sub_product: %v", err)
	}
	sf, err := repo.UpsertSubFlow(ctx, sp.ID, flowName)
	if err != nil {
		t.Fatalf("upsert sub_flow: %v", err)
	}
	return sp.Slug, sf.Slug, sf.ID
}

func TestHandleSubFlowDRDTicket_FirstTimeBootstraps(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	spSlug, sfSlug, sfID := seedSubFlowForServer(t, srv, tA, "Wallet", "M2M Settlement")
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}

	r := requestWithClaims(http.MethodPost,
		"/v1/sub-flows/"+spSlug+"/"+sfSlug+"/drd/ticket", nil, claims)
	r.SetPathValue("sub_product_slug", spSlug)
	r.SetPathValue("sub_flow_slug", sfSlug)
	w := httptest.NewRecorder()
	srv.HandleSubFlowDRDTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Ticket   string `json:"ticket"`
		TraceID  string `json:"trace_id"`
		FlowID   string `json:"flow_id"`
		TenantID string `json:"tenant_id"`
		UserID   string `json:"user_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Ticket == "" || resp.FlowID == "" {
		t.Fatal("expected non-empty ticket + flow_id")
	}
	if resp.TraceID != "drd:"+resp.FlowID {
		t.Errorf("trace_id=%q want drd:%s", resp.TraceID, resp.FlowID)
	}
	if resp.TenantID != tA || resp.UserID != uA {
		t.Errorf("identity mismatch: tenant=%s user=%s", resp.TenantID, resp.UserID)
	}

	// flow_drd row now bound to this sub_flow.
	var n int
	if err := srv.deps.DB.DB.QueryRow(
		`SELECT COUNT(*) FROM flow_drd WHERE tenant_id = ? AND sub_flow_id = ?`,
		tA, sfID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 flow_drd row after first-time bootstrap, got %d", n)
	}
}

func TestHandleSubFlowDRDTicket_HappyPathReusesExistingChain(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	spSlug, sfSlug, sfID := seedSubFlowForServer(t, srv, tA, "INDstocks", "Watchlist")
	repo := NewTenantRepo(srv.deps.DB.DB, tA)

	// Pre-populate the chain so this call hits the fast path.
	preFlowID, err := repo.ResolveFlowIDForSubFlow(context.Background(), sfID, uA)
	if err != nil {
		t.Fatalf("pre-resolve: %v", err)
	}

	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}
	r := requestWithClaims(http.MethodPost,
		"/v1/sub-flows/"+spSlug+"/"+sfSlug+"/drd/ticket", nil, claims)
	r.SetPathValue("sub_product_slug", spSlug)
	r.SetPathValue("sub_flow_slug", sfSlug)
	w := httptest.NewRecorder()
	srv.HandleSubFlowDRDTicket(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Ticket  string `json:"ticket"`
		FlowID  string `json:"flow_id"`
		TraceID string `json:"trace_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.FlowID != preFlowID {
		t.Errorf("flow_id drift: response=%s pre-bootstrapped=%s", resp.FlowID, preFlowID)
	}

	// Ticket redeems cleanly with the trace_id we expect.
	uid, tid, trace, ok := srv.deps.Tickets.RedeemTicket(resp.Ticket)
	if !ok || uid != uA || tid != tA || trace != "drd:"+preFlowID {
		t.Errorf("redeem mismatch: %s/%s/%s ok=%v", uid, tid, trace, ok)
	}
}

func TestHandleSubFlowDRDTicket_UnknownSlug(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}

	r := requestWithClaims(http.MethodPost,
		"/v1/sub-flows/no-such-product/no-such-flow/drd/ticket", nil, claims)
	r.SetPathValue("sub_product_slug", "no-such-product")
	r.SetPathValue("sub_flow_slug", "no-such-flow")
	w := httptest.NewRecorder()
	srv.HandleSubFlowDRDTicket(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSubFlowDRDTicket_CrossTenant(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	spSlug, sfSlug, _ := seedSubFlowForServer(t, srv, tA, "Wallet", "Activation")

	// Tenant B claims hitting tenant A's slug → 404 (no existence oracle).
	tenantB := uuid.NewString()
	claims := &auth.Claims{Sub: uA, Tenants: []string{tenantB}, Role: "editor"}
	r := requestWithClaims(http.MethodPost,
		"/v1/sub-flows/"+spSlug+"/"+sfSlug+"/drd/ticket", nil, claims)
	r.SetPathValue("sub_product_slug", spSlug)
	r.SetPathValue("sub_flow_slug", sfSlug)
	w := httptest.NewRecorder()
	srv.HandleSubFlowDRDTicket(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant: expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// ─── HandleSubFlowForLeaf (plan 005 U1) ──────────────────────────────────────

// seedFlowWithSubFlowBinding glues together a project + flow + sub_flow
// triple so HandleSubFlowForLeaf can resolve the binding. Returns the
// project's slug, the flow_id, and the sub_flow_id.
func seedFlowWithSubFlowBinding(
	t *testing.T,
	srv *Server,
	tenantID, userID, productName, flowName string,
) (slug, flowID, subFlowID string) {
	t.Helper()
	repo := NewTenantRepo(srv.deps.DB.DB, tenantID)
	ctx := context.Background()

	sp, err := repo.UpsertSubProduct(ctx, productName)
	if err != nil {
		t.Fatalf("sub_product: %v", err)
	}
	sf, err := repo.UpsertSubFlow(ctx, sp.ID, flowName)
	if err != nil {
		t.Fatalf("sub_flow: %v", err)
	}
	sectionID := "sec:" + sf.ID
	if err := repo.LinkSubFlowToFigmaSection(ctx, sf.ID, sectionID); err != nil {
		t.Fatalf("link section: %v", err)
	}

	p, err := repo.UpsertProject(ctx, Project{
		Name: productName + "-" + flowName, Platform: "mobile",
		Product: productName, Path: flowName, OwnerUserID: userID,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	f, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: "F-" + sf.ID, SectionID: &sectionID, Name: "FlowA",
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	return p.Slug, f.ID, sf.ID
}

func TestHandleSubFlowForLeaf_HappyPath(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	slug, flowID, sfID := seedFlowWithSubFlowBinding(t, srv, tA, uA, "Wallet", "M2M Settlement")
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}

	r := requestWithClaims(http.MethodGet,
		"/v1/projects/"+slug+"/flows/"+flowID+"/sub-flow", nil, claims)
	r.SetPathValue("slug", slug)
	r.SetPathValue("flow_id", flowID)
	w := httptest.NewRecorder()
	srv.HandleSubFlowForLeaf(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ID               string  `json:"id"`
		FullSlug         string  `json:"full_slug"`
		Name             string  `json:"name"`
		CanvasLifecycle  string  `json:"canvas_lifecycle"`
		PrototypeURL     *string `json:"prototype_url,omitempty"`
		FigmaSectionID   *string `json:"figma_section_id,omitempty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != sfID {
		t.Errorf("id=%s want %s", resp.ID, sfID)
	}
	if resp.FullSlug != "wallet/m2m-settlement" {
		t.Errorf("full_slug=%s want wallet/m2m-settlement", resp.FullSlug)
	}
	if resp.Name != "M2M Settlement" {
		t.Errorf("name=%s want M2M Settlement", resp.Name)
	}
	// No prototype + section linked → design-shipped lifecycle.
	if resp.CanvasLifecycle != "design-shipped" {
		t.Errorf("canvas_lifecycle=%s want design-shipped", resp.CanvasLifecycle)
	}
	if resp.PrototypeURL != nil {
		t.Errorf("prototype_url should be nil, got %v", *resp.PrototypeURL)
	}
	if resp.FigmaSectionID == nil || *resp.FigmaSectionID != "sec:"+sfID {
		t.Errorf("figma_section_id mismatch: %v", resp.FigmaSectionID)
	}
}

func TestHandleSubFlowForLeaf_NoBinding(t *testing.T) {
	// Flow exists, has no sub_flow bound → 404 cleanly (legacy flow).
	srv, tA, uA, _, _ := newTestServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	ctx := context.Background()

	p, err := repo.UpsertProject(ctx, Project{
		Name: "Legacy", Platform: "mobile", Product: "Plutus",
		Path: "L", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	// Flow without a section_id (legacy / freeform).
	f, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: "F-legacy", Name: "Legacy",
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}

	r := requestWithClaims(http.MethodGet,
		"/v1/projects/"+p.Slug+"/flows/"+f.ID+"/sub-flow", nil, claims)
	r.SetPathValue("slug", p.Slug)
	r.SetPathValue("flow_id", f.ID)
	w := httptest.NewRecorder()
	srv.HandleSubFlowForLeaf(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for no-binding, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSubFlowForLeaf_UnknownFlow(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	slug, _, _ := seedFlowWithSubFlowBinding(t, srv, tA, uA, "Wallet", "Activation")
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}

	r := requestWithClaims(http.MethodGet,
		"/v1/projects/"+slug+"/flows/nope/sub-flow", nil, claims)
	r.SetPathValue("slug", slug)
	r.SetPathValue("flow_id", "nope")
	w := httptest.NewRecorder()
	srv.HandleSubFlowForLeaf(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown flow, got %d", w.Code)
	}
}

func TestHandleSubFlowForLeaf_CrossTenant(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	slug, flowID, _ := seedFlowWithSubFlowBinding(t, srv, tA, uA, "Wallet", "Periodic")

	tenantB := uuid.NewString()
	claims := &auth.Claims{Sub: uA, Tenants: []string{tenantB}, Role: "editor"}

	r := requestWithClaims(http.MethodGet,
		"/v1/projects/"+slug+"/flows/"+flowID+"/sub-flow", nil, claims)
	r.SetPathValue("slug", slug)
	r.SetPathValue("flow_id", flowID)
	w := httptest.NewRecorder()
	srv.HandleSubFlowForLeaf(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSubFlowForLeaf_NoClaims(t *testing.T) {
	srv, _, _, _, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/projects/x/flows/y/sub-flow", nil)
	r.SetPathValue("slug", "x")
	r.SetPathValue("flow_id", "y")
	w := httptest.NewRecorder()
	srv.HandleSubFlowForLeaf(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleSubFlowForLeaf_MethodNotAllowed(t *testing.T) {
	srv, tA, uA, _, _ := newTestServer(t)
	claims := &auth.Claims{Sub: uA, Tenants: []string{tA}, Role: "editor"}
	r := requestWithClaims(http.MethodPost, "/v1/projects/x/flows/y/sub-flow", nil, claims)
	r.SetPathValue("slug", "x")
	r.SetPathValue("flow_id", "y")
	w := httptest.NewRecorder()
	srv.HandleSubFlowForLeaf(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleProjectEvents_RejectsInvalidTicket(t *testing.T) {
	srv, _, _, _, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/projects/x/events?ticket=nope", nil)
	r.SetPathValue("slug", "x")
	w := httptest.NewRecorder()
	srv.HandleProjectEvents(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", w.Code)
	}
}
