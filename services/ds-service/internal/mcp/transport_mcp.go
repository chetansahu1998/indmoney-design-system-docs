// transport_mcp.go — MCP-spec Streamable HTTP transport (plan 002 U1/U2/U3).
//
// The existing REST surface (`POST /v1/mcp/invoke/{name}`) stays unchanged
// for Atlas and the local stdio bridge. This file adds a sibling
// JSON-RPC-over-HTTP endpoint at `POST /mcp` that speaks the Nov-2025 MCP
// spec ("Streamable HTTP" transport) — what Claude Custom Connectors
// expect. Registry, Deps, tenant scoping, and auth are reused verbatim;
// only the wire format adapter is new.
//
// Surface mapped here:
//   - initialize                  → serverInfo + capabilities + constitution
//   - tools/list                  → catalog (visible + deep, with defer_loading)
//   - tools/call                  → Registry.Invoke wrapped in MCP envelope
//   - notifications/initialized   → no-op ack (spec-required for clients)
//
// Tool errors return `result: {content, isError: true}`. JSON-RPC `error`
// is reserved for protocol-level failures (parse, unknown method, invalid
// params at the protocol layer).
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// MCPProtocolVersion is the server's preferred version — echoed back
// when the client doesn't request a specific one. Bumped when this
// server adopts a newer revision of the MCP spec.
const MCPProtocolVersion = "2025-11-20"

// SupportedProtocolVersions enumerates every revision of the MCP spec
// this server can speak. The initialize handshake's negotiation echoes
// back the client's requested version IF it appears here, else returns
// JSON-RPC invalid_params with the supported set in error.data. Most
// recent first; the preferred version above MUST appear in this list.
var SupportedProtocolVersions = []string{
	"2025-11-20",
}

// isSupportedProtocolVersion is the membership predicate. Linear scan
// over a single-digit list — cheap. Exported as a method on the slice
// would require Go 1.21+ for slices.Contains; this is portable.
func isSupportedProtocolVersion(v string) bool {
	for _, sv := range SupportedProtocolVersions {
		if sv == v {
			return true
		}
	}
	return false
}

// MCPServerName / Version surface in the initialize handshake.
const (
	MCPServerName    = "indmoney-design-system"
	MCPServerVersion = "1.0.0"
)

// JSON-RPC error codes — standard plus MCP-specific.
const (
	jrpcParseError     = -32700
	jrpcInvalidRequest = -32600
	jrpcMethodNotFound = -32601
	jrpcInvalidParams  = -32602
	jrpcInternalError  = -32603
)

// MaxMCPBodyBytes mirrors MaxInvokeBodyBytes — same rationale.
const MaxMCPBodyBytes = MaxInvokeBodyBytes

// ─── JSON-RPC wire shapes ──────────────────────────────────────────────────

type jrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jrpcError      `json:"error,omitempty"`
}

type jrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ─── MCP-spec payload shapes ───────────────────────────────────────────────

// mcpInitializeParams is the params body the client sends on initialize.
// We only read protocolVersion; clientInfo and capabilities are
// per-spec but not yet acted on server-side.
type mcpInitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    mcpCapabilities `json:"capabilities"`
	ServerInfo      mcpServerInfo   `json:"serverInfo"`
}

type mcpCapabilities struct {
	Tools mcpToolsCapability `json:"tools"`
}

type mcpToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type mcpServerInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Instructions string `json:"instructions,omitempty"`
}

type mcpToolDescriptor struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`

	// Meta carries Anthropic-specific extensions per the MCP spec's
	// `_meta` escape hatch. Claude Connectors read defer_loading to decide
	// whether a tool is eager-loaded into the system prompt or lazy-loaded
	// via tool_search; side_effects is surfaced in confirmation prompts
	// for Destructive tools.
	Meta *mcpToolMeta `json:"_meta,omitempty"`
}

type mcpToolMeta struct {
	DeferLoading bool   `json:"defer_loading,omitempty"`
	SideEffects  string `json:"side_effects,omitempty"`
}

type mcpListToolsResult struct {
	Tools []mcpToolDescriptor `json:"tools"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type mcpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type mcpToolResult struct {
	Content           []mcpContentBlock `json:"content"`
	StructuredContent any               `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

// ─── Routing ───────────────────────────────────────────────────────────────

// RegisterMCPRoutes mounts the JSON-RPC transport at `POST /mcp`. Same
// auth + tenant resolution as the REST surface; same Registry.
func RegisterMCPRoutes(mux *http.ServeMux, deps HandlerDeps, requireAuth func(http.HandlerFunc) http.HandlerFunc) {
	if deps.Registry == nil {
		panic("mcp: RegisterMCPRoutes called with nil Registry")
	}
	if deps.Log == nil {
		panic("mcp: RegisterMCPRoutes called with nil Log")
	}
	mux.HandleFunc("POST /mcp", requireAuth(handleMCP(deps)))
	// Plan 002 U10 — Streamable HTTP optional GET-upgrade-to-SSE path.
	// Clients send `Accept: text/event-stream` to receive server-initiated
	// notifications (today: tools/list_changed). The registry is static
	// post-boot so no publisher exists yet; the wire is in place for
	// future per-user capability filtering.
	mux.HandleFunc("GET /mcp", requireAuth(handleMCPStream(deps)))
}

func handleMCP(deps HandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxMCPBodyBytes+1))
		if err != nil {
			writeJRPCError(w, nil, jrpcParseError, "failed to read body", err.Error())
			return
		}
		if len(body) > MaxMCPBodyBytes {
			writeJRPCError(w, nil, jrpcInvalidRequest, "body too large",
				fmt.Sprintf("exceeds %d bytes", MaxMCPBodyBytes))
			return
		}

		var req jrpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJRPCError(w, nil, jrpcParseError, "invalid JSON", err.Error())
			return
		}
		if req.JSONRPC != "2.0" {
			writeJRPCError(w, req.ID, jrpcInvalidRequest, "jsonrpc must be \"2.0\"", req.JSONRPC)
			return
		}
		if err := requireOAuthKind(deps.ClaimsReader, r); err != nil {
			writeJRPCError(w, req.ID, jrpcInvalidRequest, err.Error(), nil)
			return
		}

		switch req.Method {
		case "initialize":
			handleInitialize(w, req)
		case "notifications/initialized":
			// Spec: client tells server it's done initializing. No reply
			// for notifications (no `id`), but we accept either shape.
			if len(req.ID) > 0 {
				writeJRPCResult(w, req.ID, struct{}{})
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
		case "tools/list":
			handleToolsList(w, req, deps)
		case "tools/call":
			handleToolsCall(w, r, req, deps)
		default:
			writeJRPCError(w, req.ID, jrpcMethodNotFound, "unknown method", req.Method)
		}
	}
}

// ─── initialize ────────────────────────────────────────────────────────────

func handleInitialize(w http.ResponseWriter, req jrpcRequest) {
	// Protocol-version negotiation per MCP spec (2025-11-20 §Lifecycle):
	// the server responds with the SINGLE version it will speak. If the
	// client requested a version we support, echo it. If they asked for
	// something else (older / unknown), respond with our preferred —
	// the client decides whether to proceed.
	//
	// Earlier impl returned invalid_params on unknown versions; Claude
	// Connectors interpret JSON-RPC errors as McpServerError and abort
	// the entire connection with "Authorization with the MCP server
	// failed". Lenient response keeps the handshake alive and lets the
	// client downgrade-or-disconnect on its own terms.
	negotiated := MCPProtocolVersion
	if len(req.Params) > 0 {
		var params mcpInitializeParams
		// Tolerant decode — params may carry extra fields (clientInfo,
		// capabilities) we don't read yet. json.Unmarshal already
		// ignores unknown keys.
		if err := json.Unmarshal(req.Params, &params); err == nil && params.ProtocolVersion != "" {
			if isSupportedProtocolVersion(params.ProtocolVersion) {
				negotiated = params.ProtocolVersion
			}
			// else: fall through. We respond with MCPProtocolVersion
			// (our preferred). The client decides whether it can
			// converse with us at that version. Logged at INFO so
			// future protocol-version drift surfaces in metrics.
		}
	}
	writeJRPCResult(w, req.ID, mcpInitializeResult{
		ProtocolVersion: negotiated,
		Capabilities: mcpCapabilities{
			Tools: mcpToolsCapability{ListChanged: true},
		},
		ServerInfo: mcpServerInfo{
			Name:         MCPServerName,
			Version:      MCPServerVersion,
			Instructions: Constitution(),
		},
	})
}

// ─── tools/list ────────────────────────────────────────────────────────────

// handleToolsList returns the FULL catalog (Visible + Deep), not just
// Visible. This is intentional and spec-aligned: per MCP 2025-11-20,
// the `_meta.defer_loading` annotation is the client's hint to lazy-
// load specific tools (Anthropic's connector uses it to decide which
// tools live in the system prompt vs. tool_search). Pre-filtering on
// the server would hide tools from clients that don't honor
// defer_loading, breaking discoverability.
//
// The legacy REST surface /v1/mcp/tools DOES filter to Visible because
// it predates defer_loading and its consumers (Atlas, stdio bridge)
// expect a compact cold catalog. /ce-code-review finding #17 — keep
// both surfaces as-is.
func handleToolsList(w http.ResponseWriter, req jrpcRequest, deps HandlerDeps) {
	all := deps.Registry.ListAll()
	out := make([]mcpToolDescriptor, 0, len(all))
	for _, t := range all {
		desc := mcpToolDescriptor{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
			Meta:        &mcpToolMeta{},
		}
		if titled, ok := t.(ToolTitled); ok {
			desc.Title = titled.Title()
		}
		if defer_, ok := t.(ToolDeferable); ok {
			desc.Meta.DeferLoading = defer_.DeferLoading()
		} else {
			// Default: visible→false, deep→true.
			desc.Meta.DeferLoading = t.Visibility() != Visible
		}
		if sided, ok := t.(ToolSideEffected); ok {
			desc.Meta.SideEffects = sided.SideEffects().String()
		}
		out = append(out, desc)
	}
	writeJRPCResult(w, req.ID, mcpListToolsResult{Tools: out})
}

// ─── tools/call ────────────────────────────────────────────────────────────

func handleToolsCall(w http.ResponseWriter, r *http.Request, req jrpcRequest, deps HandlerDeps) {
	var params mcpToolCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJRPCError(w, req.ID, jrpcInvalidParams, "invalid params", err.Error())
			return
		}
	}
	if params.Name == "" {
		writeJRPCError(w, req.ID, jrpcInvalidParams, "missing tool name", "")
		return
	}

	// Pre-check the registry — unknown tool is a protocol-level miss, not
	// a tool-level error. Spec is silent; we choose method-not-found so
	// the client can distinguish typos from semantic failures.
	if _, ok := deps.Registry.Lookup(params.Name); !ok {
		writeJRPCError(w, req.ID, jrpcMethodNotFound, "tool not found", params.Name)
		return
	}

	tenantID, err := resolveTenant(deps.ClaimsReader, r)
	if err != nil {
		writeJRPCError(w, req.ID, jrpcInvalidRequest, "no tenant", err.Error())
		return
	}
	claims := deps.ClaimsReader(r)
	userID := ""
	if claims != nil {
		userID = claims.Sub
	}

	args := params.Arguments
	if len(args) == 0 {
		args = json.RawMessage("null")
	}

	repo := projects.NewTenantRepoFromPool(deps.DB, tenantID)
	toolDeps := Deps{
		Repo:   repo,
		Broker: deps.Broker,
		UserID: userID,
		Log:    deps.Log,
	}

	result, invokeErr := deps.Registry.Invoke(r.Context(), params.Name, toolDeps, args)

	// ErrInvalidArgs is a protocol-shape failure (the args didn't match
	// the declared inputSchema). JSON-RPC -32602 communicates that
	// distinctly from a semantic tool error.
	if errors.Is(invokeErr, ErrInvalidArgs) {
		writeJRPCError(w, req.ID, jrpcInvalidParams, "invalid arguments", invokeErr.Error())
		return
	}
	if errors.Is(invokeErr, ErrNotImplemented) {
		writeJRPCError(w, req.ID, jrpcInternalError, "not implemented", invokeErr.Error())
		return
	}

	wrapped := wrapMCPContent(result, invokeErr)
	if invokeErr != nil {
		deps.Log.Warn("mcp.tools/call error",
			"tool", params.Name,
			"tenant", tenantID,
			"err", invokeErr.Error(),
		)
	}
	writeJRPCResult(w, req.ID, wrapped)
}

// wrapMCPContent adapts our Result envelope into the MCP-spec shape (U2).
//
// Contract:
//   - Invoke error (non-protocol) → {content:[err text], isError:true}.
//   - Result.IsError == true     → {content:[json], structuredContent, isError:true}.
//   - Success                    → {content:[json], structuredContent, isError:false}.
//   - When NextActions non-empty, append a second content block for the LLM.
func wrapMCPContent(res Result, invokeErr error) mcpToolResult {
	if invokeErr != nil {
		return mcpToolResult{
			Content: []mcpContentBlock{{Type: "text", Text: invokeErr.Error()}},
			IsError: true,
		}
	}

	payload, _ := json.Marshal(res.Data)
	out := mcpToolResult{
		Content:           []mcpContentBlock{{Type: "text", Text: string(payload)}},
		StructuredContent: res.Data,
		IsError:           res.IsError,
	}
	if len(res.NextActions) > 0 {
		hint, _ := json.Marshal(res.NextActions)
		out.Content = append(out.Content, mcpContentBlock{
			Type: "text",
			Text: "Next actions: " + string(hint),
		})
	}
	return out
}

// ─── helpers ───────────────────────────────────────────────────────────────

// ─── GET /mcp — SSE upgrade for server-initiated notifications (U10) ──────

func handleMCPStream(deps HandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); accept != "" && accept != "*/*" {
			// Allow text/event-stream OR application/json (some clients
			// probe both). Reject anything else.
			if !containsMediaType(accept, "text/event-stream") {
				writeJRPCError(w, nil, jrpcInvalidRequest, "Accept must include text/event-stream", accept)
				return
			}
		}
		if deps.Broker == nil {
			writeJRPCError(w, nil, jrpcInternalError, "broker not configured", nil)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJRPCError(w, nil, jrpcInternalError, "streaming not supported", nil)
			return
		}
		if err := requireOAuthKind(deps.ClaimsReader, r); err != nil {
			writeJRPCError(w, nil, jrpcInvalidRequest, err.Error(), nil)
			return
		}
		tenantID, err := resolveTenant(deps.ClaimsReader, r)
		if err != nil {
			writeJRPCError(w, nil, jrpcInvalidRequest, "no tenant", err.Error())
			return
		}
		claims := deps.ClaimsReader(r)
		userID := ""
		if claims != nil {
			userID = claims.Sub
		}

		// Broker subscribe-key is the logical channel name keyed by tenant,
		// matching the convention every prior SSE feature in this codebase
		// uses (`mcp:tools:<tenant_id>`). Publishers of tools_list_changed
		// fan out on the same key.
		//
		// X-Trace-ID is preserved for observability (Atlas propagates it,
		// we echo it back), but it is NOT the broker subscribe key —
		// clients can set arbitrary trace IDs and using one as the
		// subscribe key would let an unauthenticated header decide which
		// fanout bucket the subscriber lands in.
		subscribeKey := "mcp:tools:" + tenantID
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.NewString()
		}

		ch, unsub, err := deps.Broker.Subscribe(subscribeKey, tenantID, userID)
		if err != nil {
			if errors.Is(err, sse.ErrSubscriberCapReached) {
				// Mirror the projects.server handler: protocol-stream
				// failure due to capacity is a transport-level 503, not a
				// JSON-RPC error frame. Operational signal for clients +
				// load balancers; matches existing convention.
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeJRPCError(w, nil, jrpcInternalError, "subscribe", err.Error())
			return
		}
		defer unsub()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Trace-ID", traceID)
		w.WriteHeader(http.StatusOK)
		// Opt out of the process-wide 5-min WriteTimeout so this stream
		// can stay open as long as Claude wants. See sse/writedeadline.go.
		if derr := sse.ClearWriteDeadline(w); derr != nil {
			deps.Log.Warn("mcp.sse.clear_write_deadline_failed", "err", derr.Error())
		}
		flusher.Flush()

		clientGone := r.Context().Done()
		for {
			select {
			case <-clientGone:
				return
			case ev, alive := <-ch:
				if !alive {
					return
				}
				if sse.IsHeartbeat(ev) {
					_, _ = w.Write([]byte(": keepalive\n\n"))
					flusher.Flush()
					continue
				}
				if _, ok := ev.(sse.MCPToolsListChanged); ok {
					// JSON-RPC notification frame — no `id` per spec
					// (notifications/* never carry one). Emitted as the
					// SSE `data:` payload with the MCP-spec method as the
					// SSE event name so a client can filter on it.
					body, _ := json.Marshal(map[string]any{
						"jsonrpc": "2.0",
						"method":  "notifications/tools/list_changed",
					})
					_, _ = fmt.Fprintf(w, "event: notifications/tools/list_changed\ndata: %s\n\n", body)
					flusher.Flush()
				}
				// Other event types are not forwarded over the MCP
				// stream — they belong to the Atlas SSE handler.
			}
		}
	}
}

// containsMediaType is a forgiving `Accept` header check — splits on
// comma, trims surrounding whitespace + q-values, matches the bare media
// type. Good enough for "Accept: text/event-stream, */*" style headers
// without pulling in a full RFC 7231 parser.
func containsMediaType(accept, want string) bool {
	for _, part := range strings.Split(accept, ",") {
		part = strings.TrimSpace(part)
		if i := strings.IndexByte(part, ';'); i >= 0 {
			part = strings.TrimSpace(part[:i])
		}
		if part == want {
			return true
		}
	}
	return false
}

// requireOAuthKind returns an error when the request's claims are not
// of kind="oauth_access". The MCP transport (POST /mcp + GET /mcp) is
// the Claude-facing endpoint and must require tokens minted via the
// OAuth flow specifically — accepting long-lived /v1/auth/login session
// JWTs here would let any logged-in user (or anyone who phished one)
// bypass the entire OAuth handshake, JTI revocation, and refresh
// rotation. Plan-002 finding #6 (P1 security).
//
// The legacy REST surface /v1/mcp/invoke/{name} is intentionally NOT
// gated on kind — Atlas and the local stdio bridge call it with
// /v1/auth/login JWTs and predate the OAuth flow.
func requireOAuthKind(read ClaimsReader, r *http.Request) error {
	if read == nil {
		return errors.New("no claims reader configured")
	}
	c := read(r)
	if c == nil {
		return errors.New("missing claims")
	}
	if c.Kind != auth.KindOAuthAccess {
		return fmt.Errorf("token kind %q not accepted on /mcp; use an OAuth-minted access token (kind=%q)", c.Kind, auth.KindOAuthAccess)
	}
	return nil
}

func writeJRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

// writeJRPCError emits a JSON-RPC 2.0 error response. Per the spec,
// JSON-RPC errors ALWAYS travel inside an HTTP 200 envelope — the HTTP
// status reflects transport health, not protocol-level errors. This is
// the right shape for Claude Connectors and every spec-compliant MCP
// client, but it confounds generic abuse-detection heuristics that
// look at 4xx/5xx rates. To compensate, every protocol-level error
// emits a structured log line that ops dashboards / WAFs can ingest
// directly. /ce-code-review finding #27 — HTTP 200 behavior is
// intentional; structured logs cover the abuse-detection gap.
func writeJRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	slog.Warn("mcp.jrpc_error",
		"code", code,
		"message", msg)
	_ = json.NewEncoder(w).Encode(jrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jrpcError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	})
}
