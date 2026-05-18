package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// tools_activity.go — /ce-code-review #5. Every prd.* mutation writes a
// prd_audit row (RecordPRDAudit), but until now no MCP tool read them.
// Claude could not answer "what changed on this sub_flow this week" even
// though prd_audit IS the canonical truth Atlas's ActivityTab reads.
// One tool: activity.list — sub_flow-scoped, newest-first, optional
// since filter, capped at 500 rows per call (the repo default).
//
// Visible + DeferLoading=false because Atlas hits it on every leaf-open
// when the Activity tab is selected (the agent should know it's there).

type activityListTool struct{}

type activityListArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	Limit       int    `json:"limit,omitempty"`
	// SinceUnix optional; when set, drops rows older than this epoch
	// second. Used to fetch "what changed since I last looked".
	SinceUnix int64 `json:"since_unix,omitempty"`
}

type activityListEntry struct {
	ID         string `json:"id"`
	PRDStateID string `json:"prd_state_id"`
	UserID     string `json:"user_id"`
	Op         string `json:"op"`
	At         string `json:"at"` // RFC3339 — easier for Claude to reason about than unix
}

type activityListResult struct {
	SubFlowSlug string              `json:"sub_flow_slug"`
	Entries     []activityListEntry `json:"entries"`
	Count       int                 `json:"count"`
	Truncated   bool                `json:"truncated"`
}

func (activityListTool) Name() string               { return "activity.list" }
func (activityListTool) Title() string              { return "List Sub-Flow Activity" }
func (activityListTool) Visibility() ToolVisibility { return Visible }
func (activityListTool) SideEffects() SideEffect    { return ReadOnly }
func (activityListTool) DeferLoading() bool         { return false }
func (activityListTool) Description() string {
	return "List the prd_audit timeline for a sub_flow: every typed-stem write (state, event, criterion, edge case, copy string, a11y note, frame attach/detach) with author and timestamp, newest-first. Use when answering 'what changed on this sub_flow recently' or auditing who touched which state. Don't use when you want the current PRD shape (call prd.get) or the resume-here view (section.inspect). Read-only; capped at 500 rows per call."
}
func (activityListTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"limit":         {"type": "integer", "description": "Max rows returned (default 500, max 500). Lower this to keep responses tight when you only need recent activity."},
			"since_unix":    {"type": "integer", "description": "Drop rows older than this unix-second timestamp. Use to fetch 'what changed since I last looked'; omit for the full window."}
		},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (activityListTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in activityListArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("activity.list: %w", err)
	}
	// Cap at 500 to bound response size; the repo applies the same cap
	// when limit <= 0. We pass through whatever the caller asked for so
	// they can shrink the window, but never amplify above 500.
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	audits, err := deps.Repo.ListPRDAuditForSubFlow(ctx, sf.ID, limit)
	if err != nil {
		return Result{}, fmt.Errorf("activity.list: %w", err)
	}
	// Apply since_unix filter in-process (the repo doesn't accept it
	// today; folding it server-side would require a new repo signature).
	entries := make([]activityListEntry, 0, len(audits))
	var sinceT time.Time
	if in.SinceUnix > 0 {
		sinceT = time.Unix(in.SinceUnix, 0)
	}
	for _, a := range audits {
		if !sinceT.IsZero() && a.At.Before(sinceT) {
			continue
		}
		entries = append(entries, activityListEntry{
			ID:         a.ID,
			PRDStateID: a.PRDStateID,
			UserID:     a.UserID,
			Op:         string(a.Op),
			At:         a.At.UTC().Format(time.RFC3339),
		})
	}
	return Result{Data: activityListResult{
		SubFlowSlug: in.SubFlowSlug,
		Entries:     entries,
		Count:       len(entries),
		// Repo capped at `limit` (=500 by default). If the raw audit
		// count equals the cap, there may be more — surface as a hint.
		Truncated: len(audits) >= limit,
	}}, nil
}
