package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// ─── Helpers ──────────────────────────────────────────────────────────────

// newTransportHandler wires a handleMCP HandlerFunc against the supplied
// harness (real DB, real registry). Tests pass tenantA in the claims by
// default; pass an override via `withClaims` to exercise auth edge cases.
func newTransportHandler(t *testing.T, h *testHarness, claims *auth.Claims) http.HandlerFunc {
	t.Helper()
	if claims == nil {
		claims = &auth.Claims{
			Sub:     h.userA,
			Email:   "a@example.com",
			Role:    "user",
			Tenants: []string{h.tenantA},
			// requireOAuthKind on POST /mcp + GET /mcp rejects anything
			// other than oauth_access (plan 002 #6). Default test claims
			// mint the right kind so individual tests don't have to.
			Kind: auth.KindOAuthAccess,
		}
	}
	return handleMCP(HandlerDeps{
		DB:           h.d,
		Broker:       nil,
		ClaimsReader: func(*http.Request) *auth.Claims { return claims },
		Registry:     h.registry,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// jrpcCall posts a JSON-RPC payload and decodes the response.
func jrpcCall(t *testing.T, handler http.HandlerFunc, method string, params any, id int) jrpcResponse {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "method": method, "id": id}
	if params != nil {
		body["params"] = params
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	rr := httptest.NewRecorder()
	handler(rr, req)

	var resp jrpcResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response (body=%q): %v", rr.Body.String(), err)
	}
	return resp
}

// resultAs decodes resp.Result (interface{}) into the typed shape via a
// JSON round-trip. Keeps the test bodies readable.
func resultAs[T any](t *testing.T, resp jrpcResponse) T {
	t.Helper()
	var out T
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode result into %T: %v", out, err)
	}
	return out
}

// ─── initialize (U1 + U3) ─────────────────────────────────────────────────

func TestTransportMCP_Initialize_ReturnsCapabilitiesAndInstructions(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	resp := jrpcCall(t, handler, "initialize", nil, 1)
	if resp.Error != nil {
		t.Fatalf("unexpected jrpc error: %+v", resp.Error)
	}
	res := resultAs[mcpInitializeResult](t, resp)

	if res.ProtocolVersion != MCPProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", res.ProtocolVersion, MCPProtocolVersion)
	}
	if !res.Capabilities.Tools.ListChanged {
		t.Error("capabilities.tools.listChanged must be true")
	}
	if res.ServerInfo.Name != MCPServerName {
		t.Errorf("serverInfo.name = %q, want %q", res.ServerInfo.Name, MCPServerName)
	}
	if res.ServerInfo.Instructions == "" {
		t.Fatal("serverInfo.instructions must be non-empty")
	}
	if !strings.Contains(res.ServerInfo.Instructions, "Slug grammar") {
		t.Error("instructions missing slug-grammar heading")
	}
	if !strings.Contains(res.ServerInfo.Instructions, "Common workflows") {
		t.Error("instructions missing workflows heading")
	}
}

func TestTransportMCP_Constitution_VersionAndLengthBudget(t *testing.T) {
	c := Constitution()
	if c == "" {
		t.Fatal("Constitution() returned empty string — //go:embed failed?")
	}
	if got := len(c); got < 1500 || got > 8000 {
		// Spec doesn't mandate a length, but the budget for
		// serverInfo.instructions is bounded by Claude's context. Keep
		// the constitution between 1.5k and 8k chars; bump the upper
		// bound deliberately when adding workflows. Bumped 6k → 8k
		// when /ce-code-review #4+#5 added the comment + activity
		// workflow sections.
		t.Errorf("Constitution length %d out of budget [1500, 8000]", got)
	}
	if ConstitutionVersion != 3 {
		t.Errorf("ConstitutionVersion = %d, want pinned 3 (bump deliberately on schema changes)", ConstitutionVersion)
	}
	// Content hash — pinning the int alone is self-referential, so we
	// fingerprint the embedded markdown too. Any edit to constitution.md
	// forces a deliberate version bump AND a re-derive of this digest;
	// either drift surfaces here.
	sum := sha256.Sum256([]byte(c))
	gotDigest := hex.EncodeToString(sum[:])
	const wantDigest = "c6117833d029de54f27e0c34ed5555980816e74435cb831be6722242c0921f72"
	if gotDigest != wantDigest {
		t.Errorf("Constitution sha256 = %s, want %s — bump ConstitutionVersion and update this digest after editing constitution.md",
			gotDigest, wantDigest)
	}
}

// TestTransportMCP_Initialize_NegotiatesProtocolVersion — finding #15
// (P1). The handshake must echo back the client's requested protocol
// version IF it's supported, or return invalid_params with the
// supported set in error.data otherwise. Hard-coding the server's
// preferred version (the pre-fix shape) breaks any client speaking an
// older revision and silently accepts any value the client sends.
func TestTransportMCP_Initialize_NegotiatesProtocolVersion(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	// 1. Client requests our preferred version — echoed back.
	resp := jrpcCall(t, handler, "initialize",
		map[string]any{"protocolVersion": MCPProtocolVersion}, 1)
	if resp.Error != nil {
		t.Fatalf("preferred-version: unexpected jrpc error: %+v", resp.Error)
	}
	res := resultAs[mcpInitializeResult](t, resp)
	if res.ProtocolVersion != MCPProtocolVersion {
		t.Errorf("preferred-version: echoed = %q, want %q", res.ProtocolVersion, MCPProtocolVersion)
	}

	// 2. Client omits protocolVersion — server defaults to preferred.
	resp = jrpcCall(t, handler, "initialize", nil, 2)
	if resp.Error != nil {
		t.Fatalf("no-version: unexpected jrpc error: %+v", resp.Error)
	}
	res = resultAs[mcpInitializeResult](t, resp)
	if res.ProtocolVersion != MCPProtocolVersion {
		t.Errorf("no-version: default = %q, want %q", res.ProtocolVersion, MCPProtocolVersion)
	}

	// 3. Client requests an unsupported version — invalid_params with
	//    the supported set in error.data.
	resp = jrpcCall(t, handler, "initialize",
		map[string]any{"protocolVersion": "2099-12-31"}, 3)
	if resp.Error == nil {
		t.Fatal("expected jrpc error for unsupported version")
	}
	if resp.Error.Code != jrpcInvalidParams {
		t.Errorf("error.code = %d, want %d (invalid_params)", resp.Error.Code, jrpcInvalidParams)
	}
	if data, ok := resp.Error.Data.(map[string]any); ok {
		if _, ok := data["supported"]; !ok {
			t.Error("error.data missing `supported` field — clients need it to adapt")
		}
	} else {
		t.Errorf("error.data shape = %T, want map", resp.Error.Data)
	}
}

// ─── notifications/initialized (U1) ───────────────────────────────────────

func TestTransportMCP_NotificationsInitialized_AcceptedSilently(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`) // no id → notification
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 Accepted for notification", rr.Code)
	}
}

// ─── tools/list (U1) ──────────────────────────────────────────────────────

func TestTransportMCP_ToolsList_ReturnsAllToolsWithMetadata(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	resp := jrpcCall(t, handler, "tools/list", nil, 2)
	if resp.Error != nil {
		t.Fatalf("unexpected jrpc error: %+v", resp.Error)
	}
	res := resultAs[mcpListToolsResult](t, resp)

	allRegistry := h.registry.ListAll()
	if len(res.Tools) != len(allRegistry) {
		t.Errorf("tools count = %d, want %d (all registered tools)", len(res.Tools), len(allRegistry))
	}

	// Cross-check: every tool has a non-empty Name, Description, inputSchema.
	for _, td := range res.Tools {
		if td.Name == "" {
			t.Error("tool with empty Name")
		}
		if td.Description == "" {
			t.Errorf("%s: empty Description", td.Name)
		}
		if len(td.InputSchema) == 0 {
			t.Errorf("%s: empty inputSchema", td.Name)
		}
	}

	// Spot-check a known Visible tool — section.inspect — has defer_loading=false.
	var inspect *mcpToolDescriptor
	for i, td := range res.Tools {
		if td.Name == "section.inspect" {
			inspect = &res.Tools[i]
			break
		}
	}
	if inspect == nil {
		t.Fatal("section.inspect not in tools/list")
	}
	if inspect.Meta == nil {
		t.Fatal("section.inspect: missing _meta")
	}
	if inspect.Meta.DeferLoading {
		t.Errorf("section.inspect: defer_loading=true, want false (Visible tool)")
	}
	if inspect.Title == "" {
		t.Error("section.inspect: missing title")
	}
	if inspect.Meta.SideEffects == "" {
		t.Error("section.inspect: missing side_effects classification")
	}
}

// ─── tools/call envelope adapter (U2) ─────────────────────────────────────

func TestTransportMCP_ToolsCall_UnknownTool_ReturnsMethodNotFound(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	resp := jrpcCall(t, handler, "tools/call", map[string]any{
		"name":      "does.not.exist",
		"arguments": map[string]any{},
	}, 3)
	if resp.Error == nil {
		t.Fatal("expected jrpc error for unknown tool")
	}
	if resp.Error.Code != jrpcMethodNotFound {
		t.Errorf("error.code = %d, want %d (method not found)", resp.Error.Code, jrpcMethodNotFound)
	}
}

func TestTransportMCP_ToolsCall_InvalidArgs_ReturnsInvalidParams(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	// section.inspect expects an object with `slug`. Passing a string
	// triggers ErrInvalidArgs from decodeArgs → JSON-RPC -32602.
	resp := jrpcCall(t, handler, "tools/call", map[string]any{
		"name":      "section.inspect",
		"arguments": "not an object",
	}, 4)
	if resp.Error == nil {
		t.Fatal("expected jrpc error for invalid args")
	}
	if resp.Error.Code != jrpcInvalidParams {
		t.Errorf("error.code = %d, want %d (invalid params)", resp.Error.Code, jrpcInvalidParams)
	}
}

func TestTransportMCP_ToolsCall_SemanticError_WrapsAsIsErrorTrue(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	// section.inspect with a non-existent slug returns projects.ErrNotFound.
	// The transport wraps that as {content, isError: true}, NOT a JSON-RPC error.
	resp := jrpcCall(t, handler, "tools/call", map[string]any{
		"name":      "section.inspect",
		"arguments": map[string]any{"sub_flow_slug": "does-not-exist/never-was"},
	}, 5)
	if resp.Error != nil {
		t.Fatalf("expected isError-wrapped result, got jrpc error: %+v", resp.Error)
	}
	res := resultAs[mcpToolResult](t, resp)
	if !res.IsError {
		t.Error("expected isError=true for missing slug")
	}
	if len(res.Content) == 0 {
		t.Error("expected at least one content block in error result")
	}
}

func TestTransportMCP_ToolsCall_Success_WrapsWithStructuredContent(t *testing.T) {
	h := newTestHarness(t)
	sf := h.seedSubFlow("test-product", "test-flow")
	_ = sf
	handler := newTransportHandler(t, h, nil)

	resp := jrpcCall(t, handler, "tools/call", map[string]any{
		"name":      "section.inspect",
		"arguments": map[string]any{"sub_flow_slug": "test-product/test-flow"},
	}, 6)
	if resp.Error != nil {
		t.Fatalf("unexpected jrpc error: %+v", resp.Error)
	}
	res := resultAs[mcpToolResult](t, resp)
	if res.IsError {
		t.Errorf("expected isError=false on happy path, got true; content=%+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("expected content blocks on success")
	}
	if res.Content[0].Type != "text" {
		t.Errorf("content[0].type = %q, want \"text\"", res.Content[0].Type)
	}
	if res.StructuredContent == nil {
		t.Error("structuredContent must be populated on success")
	}
}

// ─── protocol-error paths (U1) ────────────────────────────────────────────

func TestTransportMCP_MalformedJSON_ReturnsParseError(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	handler(rr, req)

	var resp jrpcResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != jrpcParseError {
		t.Errorf("expected parse error, got %+v", resp.Error)
	}
}

func TestTransportMCP_UnknownMethod_ReturnsMethodNotFound(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	resp := jrpcCall(t, handler, "totally/madeup", nil, 7)
	if resp.Error == nil || resp.Error.Code != jrpcMethodNotFound {
		t.Errorf("expected method-not-found, got %+v", resp.Error)
	}
}

// ─── defer_loading wire shape (U9) ────────────────────────────────────────

// TestTransportMCP_ToolsList_MetaShapeAndDeferLoading enforces the MCP-spec
// `_meta` shape: every tool descriptor carries a nested `_meta` object
// (not flat `_meta.x` dot-keys), and Visible/Deep tools default
// defer_loading correctly. Anthropic's Connector client reads this to
// decide tool_search eligibility — getting the shape wrong silently
// degrades to eager-load.
func TestTransportMCP_ToolsList_MetaShapeAndDeferLoading(t *testing.T) {
	h := newTestHarness(t)
	handler := newTransportHandler(t, h, nil)

	// Use the raw JSON body — we want to assert on the wire shape, not
	// the typed Go struct that happens to deserialize.
	body := []byte(`{"jsonrpc":"2.0","method":"tools/list","id":42}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	var envelope struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Result.Tools) == 0 {
		t.Fatal("no tools returned")
	}

	var visibleSeen, deepSeen int
	for _, td := range envelope.Result.Tools {
		name, _ := td["name"].(string)

		// Reject the legacy flat-key shape outright.
		if _, bad := td["_meta.defer_loading"]; bad {
			t.Errorf("%s: emits flat key `_meta.defer_loading` — must be nested under `_meta`", name)
		}
		if _, bad := td["_meta.side_effects"]; bad {
			t.Errorf("%s: emits flat key `_meta.side_effects` — must be nested under `_meta`", name)
		}

		metaRaw, ok := td["_meta"]
		if !ok {
			t.Errorf("%s: missing `_meta` object", name)
			continue
		}
		meta, ok := metaRaw.(map[string]any)
		if !ok {
			t.Errorf("%s: `_meta` must be an object, got %T", name, metaRaw)
			continue
		}
		// SideEffects must always be set (every tool implements
		// ToolSideEffected per U4).
		if se, _ := meta["side_effects"].(string); se == "" {
			t.Errorf("%s: _meta.side_effects must be non-empty", name)
		}
		// DeferLoading is omitempty — present as false defaults to absent
		// in JSON. Parse from the registry to compare expected vs actual.
		tool, _ := h.registry.Lookup(name)
		if tool == nil {
			continue
		}
		expectedDefer := tool.DeferLoading()
		actualDefer, _ := meta["defer_loading"].(bool)
		if expectedDefer != actualDefer {
			t.Errorf("%s: _meta.defer_loading = %v, want %v", name, actualDefer, expectedDefer)
		}
		if tool.Visibility() == Visible {
			visibleSeen++
		} else {
			deepSeen++
		}
	}
	if visibleSeen == 0 || deepSeen == 0 {
		t.Errorf("expected both Visible and Deep tools in catalog; visible=%d deep=%d", visibleSeen, deepSeen)
	}
}

// ─── envelope adapter unit tests (U2) ─────────────────────────────────────

func TestWrapMCPContent_InvokeError_SetsIsErrorTrue(t *testing.T) {
	out := wrapMCPContent(Result{}, ErrToolNotFound)
	if !out.IsError {
		t.Error("invoke error must set isError=true")
	}
	if len(out.Content) != 1 || out.Content[0].Type != "text" {
		t.Errorf("expected single text content, got %+v", out.Content)
	}
}

func TestWrapMCPContent_ResultIsError_PropagatesFlag(t *testing.T) {
	out := wrapMCPContent(Result{Data: map[string]string{"error": "boom"}, IsError: true}, nil)
	if !out.IsError {
		t.Error("Result.IsError must propagate to envelope")
	}
	if out.StructuredContent == nil {
		t.Error("structuredContent should still populate on isError per spec")
	}
}

func TestWrapMCPContent_NextActions_AppendedAsExtraContentBlock(t *testing.T) {
	out := wrapMCPContent(Result{
		Data: map[string]string{"ok": "yes"},
		NextActions: []NextAction{
			{Tool: "prd.add_state", When: "next"},
		},
	}, nil)
	if len(out.Content) != 2 {
		t.Fatalf("expected 2 content blocks (data + next_actions), got %d", len(out.Content))
	}
	if !strings.Contains(out.Content[1].Text, "Next actions") {
		t.Errorf("content[1] should carry next-actions hint, got %q", out.Content[1].Text)
	}
}

// ─── tools/list_changed SSE (U10) ─────────────────────────────────────────

// TestTransportMCP_Stream_PublishedEventReachesSubscriber wires a real
// MemoryBroker, opens GET /mcp as an SSE stream, publishes an
// MCPToolsListChanged event on the subscriber's trace id, and asserts
// the JSON-RPC notification frame is written to the response within 1s.
func TestTransportMCP_Stream_PublishedEventReachesSubscriber(t *testing.T) {
	h := newTestHarness(t)
	broker := sse.NewMemoryBroker(sse.BrokerOptions{Heartbeat: time.Hour})

	deps := HandlerDeps{
		DB:           h.d,
		Broker:       broker,
		ClaimsReader: func(*http.Request) *auth.Claims { return &auth.Claims{Sub: h.userA, Tenants: []string{h.tenantA}, Kind: auth.KindOAuthAccess} },
		Registry:     h.registry,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	noAuth := func(fn http.HandlerFunc) http.HandlerFunc { return fn }
	RegisterMCPRoutes(mux, deps, noAuth)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	traceID := "u10-test-trace"
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Trace-ID", traceID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /mcp: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Give the handler a beat to wire up Subscribe, then publish. The
	// MCP stream subscribes on the logical channel `mcp:tools:<tenant>`,
	// not on the request's X-Trace-ID (which clients can spoof). Match
	// that here so the test exercises the same fanout key the
	// notifications/tools/list_changed publisher would use in production.
	time.Sleep(50 * time.Millisecond)
	broker.Publish("mcp:tools:"+h.tenantA, sse.MCPToolsListChanged{Tenant: h.tenantA})

	// Read up to ~1s, looking for the JSON-RPC notification frame.
	type chunkErr struct {
		body []byte
		err  error
	}
	done := make(chan chunkErr, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := resp.Body.Read(buf)
		done <- chunkErr{body: buf[:n], err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil && got.err != io.EOF {
			t.Fatalf("read SSE: %v", got.err)
		}
		body := string(got.body)
		if !strings.Contains(body, "notifications/tools/list_changed") {
			t.Errorf("did not see JSON-RPC notification in body, got:\n%s", body)
		}
		if !strings.Contains(body, "event: notifications/tools/list_changed") {
			t.Errorf("did not see SSE event-type header, got:\n%s", body)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive notification within 1s")
	}
}

// TestTransportMCP_Stream_RejectsNonEventStreamAccept asserts the GET
// upgrade rejects clients that don't ask for text/event-stream.
func TestTransportMCP_Stream_RejectsNonEventStreamAccept(t *testing.T) {
	h := newTestHarness(t)
	broker := sse.NewMemoryBroker(sse.BrokerOptions{Heartbeat: time.Hour})
	deps := HandlerDeps{
		DB:           h.d,
		Broker:       broker,
		ClaimsReader: func(*http.Request) *auth.Claims { return &auth.Claims{Sub: h.userA, Tenants: []string{h.tenantA}, Kind: auth.KindOAuthAccess} },
		Registry:     h.registry,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	handler := handleMCPStream(deps)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Accept", "application/xml")
	rr := httptest.NewRecorder()
	handler(rr, req)

	var resp jrpcResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != jrpcInvalidRequest {
		t.Errorf("expected invalid-request error, got %+v", resp.Error)
	}
}
