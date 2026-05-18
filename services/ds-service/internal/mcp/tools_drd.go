package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

var _ = projects.ErrSubFlowNotFound // keep import alive across edits

// tools_drd.go — DRD deep tools. drd.read is also a Visible meta-verb
// (see meta.go); these three tools sit alongside it as the deep mutators.
//
//   drd.append            — seed-or-append the DRD ydoc state for a sub_flow.
//   drd.attach_prototype  — attach an HTML prototype URL (U3b).
//   drd.detach_prototype  — clear it.

// ─── drd.append ────────────────────────────────────────────────────────────

type drdAppendTool struct{}

type drdAppendArgs struct {
	SubFlowSlug       string `json:"sub_flow_slug"`
	ContentBytesB64   string `json:"content_bytes_base64"`
	UserID            string `json:"user_id,omitempty"`
}

type drdAppendResult struct {
	FlowID   string `json:"flow_id"`
	Revision int64  `json:"revision"`
	Bytes    int    `json:"bytes_persisted"`
}

func (drdAppendTool) Name() string               { return "drd.append" }
func (drdAppendTool) Visibility() ToolVisibility { return Deep }
func (drdAppendTool) Title() string              { return "Append DRD Snapshot" }
func (drdAppendTool) SideEffects() SideEffect    { return Mutating }
func (drdAppendTool) DeferLoading() bool         { return true }
func (drdAppendTool) Description() string {
	return "Seed or append a YDoc snapshot to the DRD for a sub_flow (orchestrates UpsertFlow → CreateDRDForSubFlow → PersistYDocSnapshotBySubFlow). Use when bootstrapping a DRD from a known YDoc payload (e.g. from a skill or an import) or recording a non-collab checkpoint. Don't use when an editor is already live on the doc — go through the Hocuspocus collab path so concurrent edits aren't clobbered. Each call writes a new revision; not idempotent."
}
func (drdAppendTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug":          {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"content_bytes_base64":   {"type": "string", "description": "Base64-encoded binary YDoc state to persist as the next snapshot."},
			"user_id":                {"type": "string", "description": "Optional override; defaults to the authenticated user threaded through Deps.UserID."}
		},
		"required": ["sub_flow_slug", "content_bytes_base64"],
		"additionalProperties": false
	}`)
}
func (drdAppendTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdAppendArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.append: %w", err)
	}
	if strings.TrimSpace(in.ContentBytesB64) == "" {
		return Result{}, fmt.Errorf("%w: content_bytes_base64 required", ErrInvalidArgs)
	}
	payload, derr := base64.StdEncoding.DecodeString(in.ContentBytesB64)
	if derr != nil {
		return Result{}, fmt.Errorf("%w: content_bytes_base64 decode: %v", ErrInvalidArgs, derr)
	}

	userID := strings.TrimSpace(in.UserID)
	if userID == "" {
		userID = deps.UserID
	}
	if userID == "" {
		return Result{}, fmt.Errorf("%w: user_id required (no authenticated user)", ErrInvalidArgs)
	}

	// Single-call orchestration via projects.BootstrapDRDForSubFlow:
	// synthetic project → per-sub_flow flow → flow_drd → snapshot.
	flowID, rev, err := deps.Repo.BootstrapDRDForSubFlow(ctx, sf.ID, userID, payload)
	if err != nil {
		return Result{}, fmt.Errorf("drd.append: %w", err)
	}

	return Result{
		Data: drdAppendResult{
			FlowID:   flowID,
			Revision: rev,
			Bytes:    len(payload),
		},
	}, nil
}

// ─── drd.attach_prototype ──────────────────────────────────────────────────

type drdAttachPrototypeTool struct{}

type drdAttachPrototypeArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
}

func (drdAttachPrototypeTool) Name() string               { return "drd.attach_prototype" }
func (drdAttachPrototypeTool) Visibility() ToolVisibility { return Deep }
func (drdAttachPrototypeTool) Title() string              { return "Attach Prototype URL" }
func (drdAttachPrototypeTool) SideEffects() SideEffect    { return Mutating }
func (drdAttachPrototypeTool) DeferLoading() bool         { return true }
func (drdAttachPrototypeTool) Description() string {
	return "Bind an HTTPS prototype URL to a sub_flow as the placeholder canvas (KTD-8). Use when the PM has a clickable prototype published before the Figma design ships and wants Atlas to render it as the canvas. Don't use when the Figma section is already bound (canvas_lifecycle=design-shipped) — the Figma frames take precedence. Idempotent on (sub_flow, url); publishes drd.prototype_attached SSE."
}
func (drdAttachPrototypeTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"url":           {"type": "string", "description": "Fully qualified https:// URL of the prototype (Figma proto, Framer, Maze, etc.)."},
			"title":         {"type": "string", "description": "Optional companion label shown beside the prototype in the canvas chrome."}
		},
		"required": ["sub_flow_slug", "url"],
		"additionalProperties": false
	}`)
}
func (drdAttachPrototypeTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdAttachPrototypeArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.attach_prototype: %w", err)
	}
	if strings.TrimSpace(in.URL) == "" {
		return Result{}, fmt.Errorf("%w: url required", ErrInvalidArgs)
	}
	if err := deps.Repo.AttachPrototype(ctx, sf.ID, strings.TrimSpace(in.URL), strings.TrimSpace(in.Title), deps.Broker); err != nil {
		return Result{}, fmt.Errorf("drd.attach_prototype: %w", err)
	}
	return Result{Data: map[string]any{
		"sub_flow_id": sf.ID,
		"url":         strings.TrimSpace(in.URL),
		"title":       strings.TrimSpace(in.Title),
		"attached_at": time.Now().UTC().Format(time.RFC3339),
	}}, nil
}

// ─── drd.detach_prototype ──────────────────────────────────────────────────

type drdDetachPrototypeTool struct{}

type drdDetachPrototypeArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

func (drdDetachPrototypeTool) Name() string               { return "drd.detach_prototype" }
func (drdDetachPrototypeTool) Visibility() ToolVisibility { return Deep }
func (drdDetachPrototypeTool) Title() string              { return "Detach Prototype URL" }
func (drdDetachPrototypeTool) SideEffects() SideEffect    { return Destructive }
func (drdDetachPrototypeTool) DeferLoading() bool         { return true }
func (drdDetachPrototypeTool) Description() string {
	return "Clear the prototype URL bound to a sub_flow (destructive: the prototype reference is gone). Use when the Figma section has shipped and the placeholder prototype should be retired, or the prototype link rotted. Don't use when you only want to swap to a new URL — drd.attach_prototype is idempotent and will overwrite. No-op when nothing is attached."
}
func (drdDetachPrototypeTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (drdDetachPrototypeTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdDetachPrototypeArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.detach_prototype: %w", err)
	}
	if err := deps.Repo.DetachPrototype(ctx, sf.ID); err != nil {
		// Surface "no row matched" as a clean no-op when there was
		// nothing to detach; the repo returns ErrSubFlowNotFound for
		// rows that vanished (zero RowsAffected).
		if errors.Is(err, projects.ErrSubFlowNotFound) {
			return Result{Data: map[string]any{"sub_flow_id": sf.ID, "detached": false}}, nil
		}
		return Result{}, fmt.Errorf("drd.detach_prototype: %w", err)
	}
	return Result{Data: map[string]any{"sub_flow_id": sf.ID, "detached": true}}, nil
}

// ─── drd.attach_anchor / detach_anchor / list_anchors (plan 005 Phase B) ───
//
// Wire BlockNote block ids to prototype screen ids so the Atlas
// PrototypeAnchorBridge can resolve a screen-click → DRD-block scroll
// deterministically (without falling back to the Phase A heuristic).

type drdAttachAnchorTool struct{}

type drdAttachAnchorArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	BlockID     string `json:"block_id"`
	ScreenID    string `json:"screen_id"`
	UserID      string `json:"user_id,omitempty"`
}

func (drdAttachAnchorTool) Name() string               { return "drd.attach_anchor" }
func (drdAttachAnchorTool) Visibility() ToolVisibility { return Deep }
func (drdAttachAnchorTool) Title() string              { return "Attach DRD Anchor" }
func (drdAttachAnchorTool) SideEffects() SideEffect    { return Mutating }
func (drdAttachAnchorTool) DeferLoading() bool         { return true }
func (drdAttachAnchorTool) Description() string {
	return "Bind a DRD BlockNote block id to a prototype screen id so the Atlas PrototypeAnchorBridge can resolve screen-click → DRD-block scroll deterministically. Use when the PM has an active prototype and wants explicit anchors instead of the Phase A heuristic. Don't use when no prototype URL is attached — drd.attach_prototype first. Idempotent on (block_id, screen_id)."
}
func (drdAttachAnchorTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"block_id":      {"type": "string", "description": "BlockNote block UUID for a DRD block (the anchor source)."},
			"screen_id":     {"type": "string", "description": "Prototype screen identifier, e.g. \"S3\" (the anchor destination)."},
			"user_id":       {"type": "string", "description": "Optional override for audit attribution; defaults to the authenticated user."}
		},
		"required": ["sub_flow_slug", "block_id", "screen_id"],
		"additionalProperties": false
	}`)
}
func (drdAttachAnchorTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdAttachAnchorArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.attach_anchor: %w", err)
	}
	user := strings.TrimSpace(in.UserID)
	if user == "" {
		user = deps.UserID
	}
	id, err := deps.Repo.AttachDRDAnchor(ctx, sf.ID, in.BlockID, in.ScreenID, user)
	if err != nil {
		return Result{}, fmt.Errorf("drd.attach_anchor: %w", err)
	}
	return Result{Data: map[string]any{
		"id":          id,
		"sub_flow_id": sf.ID,
		"block_id":    strings.TrimSpace(in.BlockID),
		"screen_id":   strings.TrimSpace(in.ScreenID),
	}}, nil
}

type drdDetachAnchorTool struct{}

type drdDetachAnchorArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	BlockID     string `json:"block_id"`
	ScreenID    string `json:"screen_id"`
}

func (drdDetachAnchorTool) Name() string               { return "drd.detach_anchor" }
func (drdDetachAnchorTool) Visibility() ToolVisibility { return Deep }
func (drdDetachAnchorTool) Title() string              { return "Detach DRD Anchor" }
func (drdDetachAnchorTool) SideEffects() SideEffect    { return Destructive }
func (drdDetachAnchorTool) DeferLoading() bool         { return true }
func (drdDetachAnchorTool) Description() string {
	return "Remove one DRD block ↔ prototype screen anchor (destructive: the binding row is deleted). Use when the PM repositioned a block or the screen id changed and the explicit anchor is now stale. Don't use when you want to inspect what's anchored — call drd.list_anchors first. No-op when the pair isn't anchored."
}
func (drdDetachAnchorTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"block_id":      {"type": "string", "description": "BlockNote block UUID for the anchor source row to detach."},
			"screen_id":     {"type": "string", "description": "Prototype screen identifier for the anchor destination to detach."}
		},
		"required": ["sub_flow_slug", "block_id", "screen_id"],
		"additionalProperties": false
	}`)
}
func (drdDetachAnchorTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdDetachAnchorArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.detach_anchor: %w", err)
	}
	if err := deps.Repo.DetachDRDAnchor(ctx, sf.ID, in.BlockID, in.ScreenID); err != nil {
		return Result{}, fmt.Errorf("drd.detach_anchor: %w", err)
	}
	return Result{Data: map[string]any{"detached": true}}, nil
}

type drdListAnchorsTool struct{}

type drdListAnchorsArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

func (drdListAnchorsTool) Name() string               { return "drd.list_anchors" }
func (drdListAnchorsTool) Visibility() ToolVisibility { return Deep }
func (drdListAnchorsTool) Title() string              { return "List DRD Anchors" }
func (drdListAnchorsTool) SideEffects() SideEffect    { return ReadOnly }
func (drdListAnchorsTool) DeferLoading() bool         { return true }
func (drdListAnchorsTool) Description() string {
	return "List every DRD block ↔ prototype screen anchor for a sub_flow. Use when the Atlas PrototypeAnchorBridge is hydrating on leaf-open, or you need to audit which blocks point at which screens. Don't use when you want the DRD content itself — call drd.read for the YDoc state. Read-only."
}
func (drdListAnchorsTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (drdListAnchorsTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdListAnchorsArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.list_anchors: %w", err)
	}
	anchors, err := deps.Repo.ListDRDAnchorsForSubFlow(ctx, sf.ID)
	if err != nil {
		return Result{}, fmt.Errorf("drd.list_anchors: %w", err)
	}
	if anchors == nil {
		anchors = []projects.DRDAnchor{}
	}
	return Result{Data: map[string]any{
		"sub_flow_id": sf.ID,
		"anchors":     anchors,
		"count":       len(anchors),
	}}, nil
}
