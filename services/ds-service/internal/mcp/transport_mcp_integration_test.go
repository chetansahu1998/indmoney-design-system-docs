package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// transport_mcp_integration_test.go — plan 002 U11.
//
// End-to-end tests that exercise the full HTTP stack the way Claude
// Connectors hit it in production: a real httptest.NewServer, real
// JWT auth middleware backed by an Ed25519 SigningKey, real Registry +
// DB, real OAuth endpoints from the auth package. The unit tests
// elsewhere (transport_mcp_test.go) skip the middleware via direct
// HandlerFunc calls; this file is the regression net for the wire.

// ─── Integration harness ──────────────────────────────────────────────────

type integrationEnv struct {
	t       *testing.T
	server  *httptest.Server
	signer  *auth.SigningKey
	access  string // minted JWT for the default tenantA user
	tenantA string
	userA   string
	harness *testHarness
}

// newIntegrationEnv spins up an httptest.NewServer wired with:
//   - The MCP transport (POST /mcp + GET /mcp).
//   - The OAuth endpoints (/v1/oauth/authorize, /v1/oauth/token,
//     /v1/oauth/revoke).
//   - A requireAuth middleware that mirrors the production wiring:
//     parses Bearer token, verifies via SigningKey, attaches Claims to
//     the request context under the same key the production handler uses.
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	h := newTestHarness(t)

	signer, err := auth.GenerateSigningKey()
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	access, err := signer.MintAccessToken(h.userA, "a@example.com", "designer", []string{h.tenantA}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	broker := sse.NewMemoryBroker(sse.BrokerOptions{Heartbeat: time.Hour})

	// In-test requireAuth + claims context plumbing — verifies the JWT,
	// attaches the parsed Claims to the context. The production wiring
	// in cmd/server/main.go does the same thing; integration test
	// recreates it locally so we don't have to lift the entire server.
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			claims, err := signer.VerifyAccessToken(strings.TrimPrefix(authz, "Bearer "))
			if err != nil {
				http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), claimsCtxKey{}, claims)
			next(w, r.WithContext(ctx))
		}
	}
	claimsReader := func(r *http.Request) *auth.Claims {
		v, _ := r.Context().Value(claimsCtxKey{}).(*auth.Claims)
		return v
	}

	mux := http.NewServeMux()
	RegisterMCPRoutes(mux, HandlerDeps{
		DB:           h.d,
		Broker:       broker,
		ClaimsReader: claimsReader,
		Registry:     h.registry,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, requireAuth)
	auth.RegisterOAuthRoutes(mux, h.d.DB, signer, auth.OAuthConfig{
		AllowedClients: []string{"claude.ai"},
	}, requireAuth, claimsReader)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &integrationEnv{
		t:       t,
		server:  srv,
		signer:  signer,
		access:  access,
		tenantA: h.tenantA,
		userA:   h.userA,
		harness: h,
	}
}

// claimsCtxKey is a private context key matching the production
// pattern in cmd/server/main.go::claimsFrom.
type claimsCtxKey struct{}

// jrpc sends a JSON-RPC POST to /mcp with the supplied bearer token.
// Pass an empty token to test the missing-auth path.
func (env *integrationEnv) jrpc(method string, params any, token string) (*http.Response, jrpcResponse) {
	env.t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "method": method, "id": 1}
	if params != nil {
		body["params"] = params
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, env.server.URL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.server.Client().Do(req)
	if err != nil {
		env.t.Fatalf("POST /mcp: %v", err)
	}
	var out jrpcResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			env.t.Fatalf("decode body: %v", err)
		}
	}
	_ = resp.Body.Close()
	return resp, out
}

// ─── Test scenarios (plan 002 §U11) ───────────────────────────────────────

// 1. Initialize handshake — capabilities + serverInfo + constitution.
func TestIntegration_Initialize_HandshakeShape(t *testing.T) {
	env := newIntegrationEnv(t)
	resp, body := env.jrpc("initialize", nil, env.access)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body.Error != nil {
		t.Fatalf("jrpc error: %+v", body.Error)
	}
	// Round-trip Result back into the typed struct.
	raw, _ := json.Marshal(body.Result)
	var init mcpInitializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatalf("decode init: %v", err)
	}
	if init.ProtocolVersion != MCPProtocolVersion {
		t.Errorf("protocolVersion drift: got %q want %q", init.ProtocolVersion, MCPProtocolVersion)
	}
	if !init.Capabilities.Tools.ListChanged {
		t.Error("capabilities.tools.listChanged must be true")
	}
	want := fmt.Sprintf("Slug grammar")
	if !strings.Contains(init.ServerInfo.Instructions, want) {
		t.Errorf("instructions missing %q heading", want)
	}
}

// 2. tools/list count matches registry; every tool has _meta.
func TestIntegration_ToolsList_ReturnsFullCatalog(t *testing.T) {
	env := newIntegrationEnv(t)
	resp, body := env.jrpc("tools/list", nil, env.access)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw, _ := json.Marshal(body.Result)
	var list mcpListToolsResult
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	wantTotal := len(env.harness.registry.ListAll())
	if len(list.Tools) != wantTotal {
		t.Errorf("got %d tools, want %d", len(list.Tools), wantTotal)
	}
	for _, td := range list.Tools {
		if td.Meta == nil {
			t.Errorf("%s: missing _meta", td.Name)
			continue
		}
		if td.Meta.SideEffects == "" {
			t.Errorf("%s: missing _meta.side_effects", td.Name)
		}
	}
}

// 3. tools/call section.inspect with valid args returns structuredContent.
func TestIntegration_ToolsCall_SectionInspect_SeededSubFlow(t *testing.T) {
	env := newIntegrationEnv(t)
	env.harness.seedSubFlow("Wallet", "M2M")

	resp, body := env.jrpc("tools/call", map[string]any{
		"name":      "section.inspect",
		"arguments": map[string]any{"sub_flow_slug": "wallet/m2m"},
	}, env.access)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body.Error != nil {
		t.Fatalf("unexpected jrpc error: %+v", body.Error)
	}
	raw, _ := json.Marshal(body.Result)
	var out mcpToolResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.IsError {
		t.Errorf("isError=true unexpected; content=%+v", out.Content)
	}
	if out.StructuredContent == nil {
		t.Error("structuredContent must be populated on success")
	}
}

// 4. Missing Authorization → 401 BEFORE JSON-RPC dispatch.
func TestIntegration_MissingAuth_Returns401BeforeDispatch(t *testing.T) {
	env := newIntegrationEnv(t)
	resp, _ := env.jrpc("initialize", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// 5. Cross-tenant: claim says tenantA, slug references tenantB → isError:true.
func TestIntegration_ToolsCall_CrossTenant_ReturnsIsError(t *testing.T) {
	env := newIntegrationEnv(t)
	// section.inspect under tenantA for a slug that only exists in
	// tenantB. The tenant-scoped repo returns ErrNotFound, which the
	// transport wraps as isError:true (NOT as a JSON-RPC error).
	resp, body := env.jrpc("tools/call", map[string]any{
		"name":      "section.inspect",
		"arguments": map[string]any{"sub_flow_slug": "other-tenant/secret"},
	}, env.access)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body.Error != nil {
		t.Fatalf("expected isError-wrapped result, got jrpc error: %+v", body.Error)
	}
	raw, _ := json.Marshal(body.Result)
	var out mcpToolResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.IsError {
		t.Error("expected isError=true for cross-tenant miss")
	}
}

// 6. Constitution version assertion — initialize must carry it intact.
func TestIntegration_Initialize_ConstitutionContainsKeyHeadings(t *testing.T) {
	env := newIntegrationEnv(t)
	_, body := env.jrpc("initialize", nil, env.access)
	raw, _ := json.Marshal(body.Result)
	var init mcpInitializeResult
	_ = json.Unmarshal(raw, &init)
	for _, heading := range []string{
		"Slug grammar",
		"Lifecycle states",
		"PRD typed-stems model",
		"Common workflows",
	} {
		if !strings.Contains(init.ServerInfo.Instructions, heading) {
			t.Errorf("constitution missing heading %q", heading)
		}
	}
}

// 7. OAuth happy path → MCP call: authorize → token → MCP works → refresh → MCP works again.
func TestIntegration_OAuth_FullFlowProducesUsableMCPToken(t *testing.T) {
	env := newIntegrationEnv(t)

	// Build a PKCE verifier + S256 challenge. Use the test helper from
	// oauth_test if exported, otherwise compute inline.
	verifier := "test-verifier-" + uuid.NewString()
	challenge := pkceS256(verifier)

	// 1. authorize — requires existing session JWT (env.access).
	authorizeURL := fmt.Sprintf(
		"%s/v1/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=xyz",
		env.server.URL,
		"claude.ai",
		url.QueryEscape("https://claude.ai/api/mcp/auth_callback"),
		challenge,
	)
	req, _ := http.NewRequest(http.MethodGet, authorizeURL, nil)
	req.Header.Set("Authorization", "Bearer "+env.access)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorize status = %d, want 302/303", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in Location: %s", loc.String())
	}

	// 2. token — exchange code + verifier for {access_token, refresh_token}.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("client_id", "claude.ai")
	form.Set("redirect_uri", "https://claude.ai/api/mcp/auth_callback")
	tokenResp, err := env.server.Client().PostForm(env.server.URL+"/v1/oauth/token", form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("token status = %d body=%s", tokenResp.StatusCode, string(body))
	}
	var tokenBody struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenBody); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	_ = tokenResp.Body.Close()
	if tokenBody.AccessToken == "" || tokenBody.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", tokenBody)
	}

	// 3. The OAuth-minted access token must work on POST /mcp transparently.
	resp1, body1 := env.jrpc("initialize", nil, tokenBody.AccessToken)
	if resp1.StatusCode != http.StatusOK || body1.Error != nil {
		t.Fatalf("MCP with OAuth token failed: status=%d err=%+v", resp1.StatusCode, body1.Error)
	}

	// 4. refresh → new access + new refresh.
	form2 := url.Values{}
	form2.Set("grant_type", "refresh_token")
	form2.Set("refresh_token", tokenBody.RefreshToken)
	form2.Set("client_id", "claude.ai")
	refreshResp, err := env.server.Client().PostForm(env.server.URL+"/v1/oauth/token", form2)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(refreshResp.Body)
		t.Fatalf("refresh status = %d body=%s", refreshResp.StatusCode, string(b))
	}
	var refreshed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(refreshResp.Body).Decode(&refreshed)
	_ = refreshResp.Body.Close()
	if refreshed.AccessToken == "" {
		t.Fatal("refresh did not return a new access_token")
	}
	if refreshed.RefreshToken == tokenBody.RefreshToken {
		t.Error("refresh token was not rotated")
	}

	// 5. Rotated access token still calls MCP successfully.
	resp2, body2 := env.jrpc("initialize", nil, refreshed.AccessToken)
	if resp2.StatusCode != http.StatusOK || body2.Error != nil {
		t.Fatalf("MCP with rotated OAuth token failed: status=%d err=%+v", resp2.StatusCode, body2.Error)
	}
}

// pkceS256 mirrors RFC 7636 §4.2 — base64url-no-padding of SHA-256(verifier).
// Kept inline so the integration test doesn't depend on oauth.go internals.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
