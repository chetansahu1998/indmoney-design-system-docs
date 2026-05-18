// Package mcp implements the in-process tool registry that backs the
// Phase 1 PM workflow surface (plan 2026-05-17-002, U6).
//
// Wire contract:
//   - The Go service speaks plain HTTP+JSON. The MCP stdio protocol lives
//     in the Node bridge (U7).
//   - GET  /v1/mcp/tools             — returns the cold catalog (Visible only).
//   - POST /v1/mcp/invoke/{name}     — invokes any registered tool by name.
//
// Tool surface (KTD-5 — progressive discovery):
//   - 3 visible meta-verbs:  drd.read, prd.author, section.inspect
//   - ~15 deep tools reachable on demand, schemas returned inline via
//     meta-verb next_actions / schema_hint.
//
// Tools delegate to existing repo methods (projects.TenantRepo). No
// business logic lives in this package. Every Invoke gets a per-request
// `Deps` carrying a tenant-scoped repo so cross-tenant data is impossible
// at the SQL site.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Tool contract ──────────────────────────────────────────────────────────

// ToolVisibility tags whether a tool surfaces in the cold catalog.
type ToolVisibility int

const (
	// Visible tools are returned by GET /v1/mcp/tools — the cold catalog
	// that Claude sees on every conversation start.
	Visible ToolVisibility = iota

	// Deep tools are invokable but not in the cold catalog. The meta-verbs
	// hand out their schemas via next_actions / schema_hint when the
	// conversation reaches the point where they matter.
	Deep
)

// SideEffect classifies what a tool does to system state. Plan 002 KTD-4 —
// the MCP-spec transport prefixes destructive tools' descriptions with
// "[destructive]" so Claude can prompt for confirmation. The interface
// method (vs. an out-of-band annotation map) is compiler-enforced — every
// new tool author must classify side effects at write time.
type SideEffect int

const (
	// ReadOnly means the tool reads ds-service state and writes nothing.
	// Examples: drd.read, prd.get, section.inspect, resolve.
	ReadOnly SideEffect = iota

	// Mutating means the tool writes new state or mutates existing rows
	// non-destructively. Examples: drd.append (appends a snapshot),
	// drd.attach_prototype (sets prototype URL), prd.add_state (inserts).
	Mutating

	// Destructive means the tool removes or invalidates state. Reversal
	// requires a separate operation. Examples: drd.detach_prototype,
	// drd.detach_anchor, prd.detach_frame.
	Destructive
)

func (s SideEffect) String() string {
	switch s {
	case ReadOnly:
		return "read-only"
	case Mutating:
		return "mutating"
	case Destructive:
		return "destructive"
	}
	return "unknown"
}

// Tool is the single-method dispatcher shape — mirrors projects.RuleRunner.
// Implementations should be stateless w.r.t. global mutable state; per-request
// dependencies (repo, broker, logger) arrive via Deps.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Visibility() ToolVisibility
	Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error)
}

// May 18, 2026: Title / SideEffects / DeferLoading were promoted to
// REQUIRED interface members in an earlier session as part of a Plan 002
// "MCP-spec compliance" migration that was never finished — only a
// handful of tools (drdReadTool, prdAuthorTool) implement them, and no
// caller actually reads them. Moving them to OPTIONAL satellite
// interfaces so the binary builds while the migration is still in
// flight; callers can type-assert when the time comes to surface the
// catalogue metadata.

// ToolTitled returns the human-readable display name for catalogue UIs.
type ToolTitled interface{ Title() string }

// ToolSideEffected classifies the tool's impact on system state.
type ToolSideEffected interface{ SideEffects() SideEffect }

// ToolDeferable lets a tool opt out of eager system-prompt loading.
type ToolDeferable interface{ DeferLoading() bool }

// Deps carries the per-request dependencies a tool needs. The HTTP handler
// builds this once per call after extracting the JWT claims and resolving
// the tenant. Tools must not stash the Repo pointer beyond the call —
// it's tenant-scoped to this request.
type Deps struct {
	// Repo is tenant-scoped via the JWT claims at the HTTP boundary.
	Repo *projects.TenantRepo

	// Broker publishes SSE events (e.g. drd.prototype_attached). May be
	// nil in CLI/test contexts; the AttachPrototype path is broker-tolerant.
	Broker projects.SubFlowEventBroker

	// UserID is the authenticated user (auth.Claims.Sub). Threaded into
	// audit + ydoc snapshot writers that record `updated_by_user_id`.
	UserID string

	// Log is the per-request structured logger. Tools that record
	// best-effort side effects (e.g. prd_audit insertion in tools_prd.go)
	// use it to surface failures without failing the user-facing tool
	// result. May be nil in test contexts; tools must guard with a
	// `slog.Default()` fallback (see toolLog helper).
	Log *slog.Logger
}

// toolLog returns a non-nil logger for tools. Centralizes the
// `slog.Default()` fallback so each tool doesn't repeat the guard.
func toolLog(d Deps) *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

// Result is the wire shape every tool returns. The HTTP handler serializes
// it as the response body; the Node bridge (U7) forwards it unchanged.
//
// `Data` is the per-tool payload. `NextActions` and `SchemaHint` are the
// progressive-discovery affordances — they let Claude walk the workflow
// without enumerating every tool name up-front.
type Result struct {
	Data        any          `json:"data"`
	NextActions []NextAction `json:"next_actions,omitempty"`
	SchemaHint  *SchemaHint  `json:"schema_hint,omitempty"`
}

// NextAction is a meta-verb hint — "after this call, the likely next step
// is to invoke tool X with op Y, here's a starter shape for the args."
type NextAction struct {
	Tool      string          `json:"tool"`
	Op        string          `json:"op,omitempty"`
	When      string          `json:"when"`
	InputHint json.RawMessage `json:"input_hint,omitempty"`
}

// SchemaHint is an inline JSON-Schema fragment for the tool the meta-verb
// recommends. Lets Claude shape the next call without a round-trip to
// /v1/mcp/tools.
type SchemaHint struct {
	Tool   string          `json:"tool"`
	Schema json.RawMessage `json:"schema"`
}

// ─── Registry ──────────────────────────────────────────────────────────────

// Registry is the in-process tool catalog. Tools are registered once at
// startup (or test construction) and dispatched per-request.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry. Callers register tools with
// Register before serving any traffic.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds one tool to the registry. Duplicate names panic — this is
// startup-time wiring; a duplicate means a copy-paste bug, not a runtime
// edge case worth tolerating.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t == nil {
		panic("mcp: nil tool registered")
	}
	name := t.Name()
	if name == "" {
		panic("mcp: tool with empty name registered")
	}
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("mcp: tool %q already registered", name))
	}
	r.tools[name] = t
}

// Lookup returns the tool by name and whether it was found.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// ListVisible returns the cold catalog — Visible tools only, sorted by
// name so the JSON is deterministic and snapshot tests are stable.
func (r *Registry) ListVisible() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if t.Visibility() == Visible {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ListAll returns every registered tool, Visible + Deep, sorted by name.
// Used by tests + tooling — never returned as a cold catalog.
func (r *Registry) ListAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Invoke dispatches to the named tool. Returns ErrToolNotFound if no tool
// is registered under that name.
func (r *Registry) Invoke(ctx context.Context, name string, deps Deps, args json.RawMessage) (Result, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}
	return t.Invoke(ctx, deps, args)
}

// ─── Sentinel errors ───────────────────────────────────────────────────────

// ErrToolNotFound is returned when Invoke / Lookup misses. Maps to HTTP 404.
var ErrToolNotFound = errors.New("mcp: tool not found")

// ErrInvalidArgs is the contract violation marker. Tool handlers wrap this
// (`fmt.Errorf("%w: ...", ErrInvalidArgs)`) so the HTTP layer can return
// 400 instead of 500 without sniffing error strings.
var ErrInvalidArgs = errors.New("mcp: invalid arguments")

// ErrNotImplemented marks deep tools that are reserved for a follow-up
// unit. section.outline_states uses this — U6b replaces the body but the
// tool name + schema are registered in U6 so the cold catalog + meta-verb
// next_actions are stable.
var ErrNotImplemented = errors.New("mcp: not implemented")

// ─── Helpers shared by tool implementations ────────────────────────────────

// decodeArgs is a small wrapper that returns ErrInvalidArgs on JSON failure.
// Tool handlers call it to keep the error-type contract consistent.
func decodeArgs(args json.RawMessage, out any) error {
	if len(args) == 0 || string(args) == "null" {
		// Empty input is allowed — the args struct stays zero-valued.
		return nil
	}
	if err := json.Unmarshal(args, out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgs, err)
	}
	return nil
}

// rawJSON is a tiny helper for inline JSON-Schema literals. Panics on bad
// input — the input is always a string literal in this package, so a panic
// here is a build-time mistake, not a runtime concern.
func rawJSON(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic("mcp: invalid JSON literal: " + s)
	}
	return json.RawMessage(s)
}
