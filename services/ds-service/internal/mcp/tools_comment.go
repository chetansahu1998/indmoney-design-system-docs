package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// tools_comment.go — /ce-code-review #4. Atlas exposes "Comment on this
// state" on every PRD state card; the server has CreateComment +
// ListCommentsForTarget; previously zero MCP coverage. Three tools:
// comment.create / comment.list / comment.delete (delete is a soft-resolve
// — comments don't hard-delete; resolution is an audit-preserving close).
//
// All three are Visible (Atlas-parity is the test) and DeferLoading=false.
// Constitution §7 names the workflow:
//   "Reply to a comment thread" → comment.list → comment.create.

// ─── comment.create ────────────────────────────────────────────────────────

type commentCreateTool struct{}

type commentCreateArgs struct {
	// TargetKind: drd_block | screen | prd_state | decision | violation | comment.
	TargetKind      string `json:"target_kind"`
	TargetID        string `json:"target_id"`
	FlowID          string `json:"flow_id,omitempty"`
	Body            string `json:"body"`
	ParentCommentID string `json:"parent_comment_id,omitempty"`
}

func (commentCreateTool) Name() string               { return "comment.create" }
func (commentCreateTool) Title() string              { return "Create Comment" }
func (commentCreateTool) Visibility() ToolVisibility { return Visible }
func (commentCreateTool) SideEffects() SideEffect    { return Mutating }
func (commentCreateTool) DeferLoading() bool         { return false }
func (commentCreateTool) Description() string {
	return "Create a comment thread anchored to a DRD block, screen, PRD state, decision, violation, or another comment (reply). Use when a PM, designer, or engineer wants to leave context on a specific artifact — the comment surfaces in Atlas's right-rail Comments tab keyed by target. Don't use when the note belongs in the PRD body itself (call prd.add_state's *_md fields). @mentions in the body fan out as notifications. Records a row in drd_comments; idempotency is at the caller (no server-side dedupe)."
}
func (commentCreateTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"target_kind":       {"type": "string", "description": "One of: drd_block, screen, prd_state, decision, violation, comment (for replies)."},
			"target_id":         {"type": "string", "description": "The id of the target row (block UUID, screen id, prd_state id, etc.). Matches the target_kind."},
			"flow_id":           {"type": "string", "description": "Optional flow id for tenant-scoped listing; carried on the row for index lookups."},
			"body":              {"type": "string", "description": "Markdown comment body, ≤4000 chars. @mentions are parsed automatically."},
			"parent_comment_id": {"type": "string", "description": "When replying, the id of the parent comment. Reply depth is currently flat (v1)."}
		},
		"required": ["target_kind", "target_id", "body"],
		"additionalProperties": false
	}`)
}
func (commentCreateTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in commentCreateArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	validated, err := projects.ValidateCommentInput(projects.CommentInput{
		TargetKind:      projects.CommentTargetKind(in.TargetKind),
		TargetID:        in.TargetID,
		FlowID:          in.FlowID,
		Body:            in.Body,
		ParentCommentID: in.ParentCommentID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidArgs, err)
	}
	rec, _, err := deps.Repo.CreateComment(ctx, deps.UserID, validated)
	if err != nil {
		return Result{}, fmt.Errorf("comment.create: %w", err)
	}
	return Result{
		Data: rec,
		NextActions: []NextAction{
			{Tool: "comment.list", When: "to re-fetch the thread after the new row lands",
				InputHint: rawJSON(`{"target_kind": "` + in.TargetKind + `", "target_id": "` + in.TargetID + `"}`)},
		},
	}, nil
}

// ─── comment.list ──────────────────────────────────────────────────────────

type commentListTool struct{}

type commentListArgs struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}

func (commentListTool) Name() string               { return "comment.list" }
func (commentListTool) Title() string              { return "List Comments" }
func (commentListTool) Visibility() ToolVisibility { return Visible }
func (commentListTool) SideEffects() SideEffect    { return ReadOnly }
func (commentListTool) DeferLoading() bool         { return false }
func (commentListTool) Description() string {
	return "List every comment anchored to a specific target (DRD block, screen, PRD state, etc.). Use when an agent needs to read the conversation around an artifact before authoring — picks up @mentions and prior reasoning. Don't use when you want a flow-wide feed (call activity.list for the audit timeline; comment.list is target-scoped). Read-only."
}
func (commentListTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"target_kind": {"type": "string", "description": "Comment anchor type — drd_block, screen, prd_state, decision, violation."},
			"target_id":   {"type": "string", "description": "The id of the target row."}
		},
		"required": ["target_kind", "target_id"],
		"additionalProperties": false
	}`)
}
func (commentListTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in commentListArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	if in.TargetKind == "" || in.TargetID == "" {
		return Result{}, fmt.Errorf("%w: target_kind + target_id required", ErrInvalidArgs)
	}
	comments, err := deps.Repo.ListCommentsForTarget(ctx, projects.CommentTargetKind(in.TargetKind), in.TargetID)
	if err != nil {
		return Result{}, fmt.Errorf("comment.list: %w", err)
	}
	return Result{Data: map[string]any{
		"target_kind": in.TargetKind,
		"target_id":   in.TargetID,
		"comments":    comments,
		"count":       len(comments),
	}}, nil
}

// ─── comment.delete (soft-resolve) ─────────────────────────────────────────

type commentDeleteTool struct{}

type commentDeleteArgs struct {
	CommentID string `json:"comment_id"`
}

func (commentDeleteTool) Name() string               { return "comment.delete" }
func (commentDeleteTool) Title() string              { return "Resolve Comment" }
func (commentDeleteTool) Visibility() ToolVisibility { return Deep }
func (commentDeleteTool) SideEffects() SideEffect    { return Destructive }
func (commentDeleteTool) DeferLoading() bool         { return true }
func (commentDeleteTool) Description() string {
	return "Soft-resolve (mark as resolved) a comment row. Use when a thread's question has been answered and the surface area should hide it from the active list. Don't use when you want to edit the body — comments are immutable; create a reply via comment.create with parent_comment_id instead. Resolution is audit-preserving (the row stays in drd_comments with resolved_at + resolved_by set). Confirm with the user before calling — this hides the thread from default views."
}
func (commentDeleteTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"comment_id": {"type": "string", "description": "The drd_comments row id to mark resolved."}
		},
		"required": ["comment_id"],
		"additionalProperties": false
	}`)
}
func (commentDeleteTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in commentDeleteArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	if in.CommentID == "" {
		return Result{}, fmt.Errorf("%w: comment_id required", ErrInvalidArgs)
	}
	// ResolveComment is the audit-preserving close. The Repo method
	// returns ErrNotFound for unknown rows and ErrForbidden for
	// cross-tenant attempts (tenant-scoped repo guards both).
	if err := deps.Repo.ResolveComment(ctx, in.CommentID, deps.UserID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			return Result{IsError: true, Data: map[string]string{"error": "comment not found"}}, nil
		}
		return Result{}, fmt.Errorf("comment.delete: %w", err)
	}
	return Result{Data: map[string]any{"comment_id": in.CommentID, "resolved": true}}, nil
}
