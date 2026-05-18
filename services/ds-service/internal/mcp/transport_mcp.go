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
	"net/http"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// MCP protocol version negotiated at initialize. Bumped when this server
// adopts a newer revision of the MCP spec.
const MCPProtocolVersion = "2025-11-20"

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

type mcpInitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    mcpCapabilities    `json:"capabilities"`
	ServerInfo      mcpServerInfo      `json:"serverInfo"`
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
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	DeferLoading bool            `json:"_meta.defer_loading,omitempty"`
	SideEffects  string          `json:"_meta.side_effects,omitempty"`
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
	writeJRPCResult(w, req.ID, mcpInitializeResult{
		ProtocolVersion: MCPProtocolVersion,
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

func handleToolsList(w http.ResponseWriter, req jrpcRequest, deps HandlerDeps) {
	all := deps.Registry.ListAll()
	out := make([]mcpToolDescriptor, 0, len(all))
	for _, t := range all {
		desc := mcpToolDescriptor{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		}
		if titled, ok := t.(ToolTitled); ok {
			desc.Title = titled.Title()
		}
		if defer_, ok := t.(ToolDeferable); ok {
			desc.DeferLoading = defer_.DeferLoading()
		} else {
			// Default: visible→false, deep→true.
			desc.DeferLoading = t.Visibility() != Visible
		}
		if sided, ok := t.(ToolSideEffected); ok {
			desc.SideEffects = sided.SideEffects().String()
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

func writeJRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeJRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	// Protocol errors map to 200 OK with a JSON-RPC error body — JSON-RPC
	// over HTTP is its own envelope; the HTTP status reflects transport
	// health, not protocol-level errors. The exception: parse errors with
	// no id sometimes return 400, but Claude Connectors accept 200.
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
