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
func (drdAppendTool) Description() string {
	return "Seed (or append a snapshot to) the DRD YDoc for a sub_flow. Orchestrates UpsertFlow → CreateDRDForSubFlow → PersistYDocSnapshotBySubFlow."
}
func (drdAppendTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug":          {"type": "string"},
			"content_bytes_base64":   {"type": "string", "description": "base64-encoded binary YDoc state"},
			"user_id":                {"type": "string", "description": "optional override; defaults to authenticated user"}
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
func (drdAttachPrototypeTool) Description() string {
	return "Attach an HTML prototype URL to a sub_flow as the placeholder canvas (KTD-8). Publishes drd.prototype_attached SSE."
}
func (drdAttachPrototypeTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"url":           {"type": "string", "description": "https:// URL of the prototype"},
			"title":         {"type": "string"}
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
func (drdDetachPrototypeTool) Description() string {
	return "Detach the prototype URL from a sub_flow. No-op when nothing is attached."
}
func (drdDetachPrototypeTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string"}},
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
func (drdAttachAnchorTool) Description() string {
	return "Bind a DRD BlockNote block id to a prototype screen id. Idempotent."
}
func (drdAttachAnchorTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"block_id":      {"type": "string", "description": "BlockNote block UUID"},
			"screen_id":     {"type": "string", "description": "Prototype screen id, e.g. S3"},
			"user_id":       {"type": "string"}
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
func (drdDetachAnchorTool) Description() string {
	return "Remove one DRD block ↔ prototype screen anchor. No-op when the pair isn't anchored."
}
func (drdDetachAnchorTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"block_id":      {"type": "string"},
			"screen_id":     {"type": "string"}
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
func (drdListAnchorsTool) Description() string {
	return "List every DRD block ↔ prototype screen anchor under a sub_flow. Atlas bridge consumes this on leaf-open."
}
func (drdListAnchorsTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string"}},
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
