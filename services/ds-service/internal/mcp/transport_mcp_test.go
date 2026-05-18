package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
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
	if got := len(c); got < 1500 || got > 6000 {
		// Spec doesn't mandate a length, but the budget for
		// serverInfo.instructions is bounded by Claude's context. Keep
		// the constitution between 1.5k and 6k chars; bump the upper
		// bound deliberately when adding workflows.
		t.Errorf("Constitution length %d out of budget [1500, 6000]", got)
	}
	if ConstitutionVersion != 1 {
		t.Errorf("ConstitutionVersion = %d, want pinned 1 (bump deliberately on schema changes)", ConstitutionVersion)
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
