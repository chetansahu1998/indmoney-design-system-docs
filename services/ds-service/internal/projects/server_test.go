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
