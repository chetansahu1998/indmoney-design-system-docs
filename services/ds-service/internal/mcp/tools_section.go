package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// tools_section.go — section-shape tools. Two deep tools live here:
//
//   section.frames         — direct-child frames of the sub_flow's bound
//                            section (wraps U5 ListSectionFrames).
//   section.outline_states — coverage-wall view; U6b territory. Schema +
//                            registration ship here so the meta-verb
//                            next_actions can stay stable, but Invoke
//                            returns ErrNotImplemented until U6b lands.

// ─── section.frames ─────────────────────────────────────────────────────────

type sectionFramesTool struct{}

type sectionFramesArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

type sectionFramesResult struct {
	SubFlowID      string            `json:"sub_flow_id"`
	FigmaSectionID *string           `json:"figma_section_id,omitempty"`
	FileKey        string            `json:"file_key,omitempty"`
	Frames         []sectionFrameRow `json:"frames"`
	Note           string            `json:"note,omitempty"`
}

// sectionFrameRow mirrors projects.FrameRow as the wire shape. We don't
// reuse the projects type so the MCP package owns the field tags (the
// projects.FrameRow struct uses Go field names without json tags today).
type sectionFrameRow struct {
	NodeID       string  `json:"figma_node_id"`
	Name         string  `json:"name"`
	ParentNodeID string  `json:"parent_node_id"`
	Depth        int     `json:"depth"`
	AbsX         float64 `json:"abs_x"`
	AbsY         float64 `json:"abs_y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	HasRender    bool    `json:"has_render"`
}

func (sectionFramesTool) Name() string               { return "section.frames" }
func (sectionFramesTool) Visibility() ToolVisibility { return Deep }
func (sectionFramesTool) Description() string {
	return "List the direct-child Figma frames of a sub_flow's bound section (designer's names, canvas y-axis order)."
}
func (sectionFramesTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "{sub_product_slug}/{sub_flow_slug}"}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (sectionFramesTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in sectionFramesArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("section.frames: %w", err)
	}
	out := sectionFramesResult{
		SubFlowID:      sf.ID,
		FigmaSectionID: sf.FigmaSectionID,
		Frames:         []sectionFrameRow{},
	}
	if sf.FigmaSectionID == nil || *sf.FigmaSectionID == "" {
		out.Note = "no figma section bound yet — check canvas_lifecycle (proto-only or empty)"
		return Result{Data: out}, nil
	}

	// We need the file_key for the bound section. Look it up via a small
	// inline query through the read pool. The projects.TenantRepo doesn't
	// expose a "section by id → file_key" helper today; rather than thread
	// one through, do a single read against figma_section. The query is
	// tenant-scoped and constant-size.
	fileKey, err := lookupSectionFileKey(ctx, deps, *sf.FigmaSectionID)
	if err != nil {
		return Result{}, fmt.Errorf("section.frames: resolve file_key: %w", err)
	}
	out.FileKey = fileKey

	frames, err := deps.Repo.ListSectionFrames(ctx, fileKey, *sf.FigmaSectionID)
	if err != nil {
		return Result{}, fmt.Errorf("section.frames: %w", err)
	}
	for _, f := range frames {
		out.Frames = append(out.Frames, sectionFrameRow{
			NodeID:       f.NodeID,
			Name:         f.Name,
			ParentNodeID: f.ParentNodeID,
			Depth:        f.Depth,
			AbsX:         f.AbsX,
			AbsY:         f.AbsY,
			Width:        f.Width,
			Height:       f.Height,
			HasRender:    f.HasRender,
		})
	}
	return Result{Data: out}, nil
}

// ─── section.outline_states (U6b stub) ─────────────────────────────────────

type sectionOutlineStatesTool struct{}

type sectionOutlineStatesArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

func (sectionOutlineStatesTool) Name() string               { return "section.outline_states" }
func (sectionOutlineStatesTool) Visibility() ToolVisibility { return Deep }
func (sectionOutlineStatesTool) Description() string {
	return "Coverage wall: every frame in the section with its binding + PRD coverage status. (Stub — implemented in U6b.)"
}
func (sectionOutlineStatesTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string"}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (sectionOutlineStatesTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	// U6b will replace this body. Keeping the registration here means the
	// meta-verb next_actions can reference section.outline_states from day
	// one without a "tool not found" surprise once U6b ships.
	return Result{}, fmt.Errorf("%w: section.outline_states is U6b territory", ErrNotImplemented)
}

// ─── helpers ───────────────────────────────────────────────────────────────

// lookupSectionFileKey returns the file_key for the bound figma_section_id.
// Uses the read pool directly via TenantRepo.UnsafeReadDB — see note below.
//
// Implementation note: the projects.TenantRepo does not expose a "get
// file_key for section id" helper today, and adding one is heavier than
// this needs. We inline a tenant-scoped read. If a follow-up unit adds a
// dedicated helper (e.g. `GetSectionFileKey`), swap it in here.
func lookupSectionFileKey(ctx context.Context, deps Deps, sectionID string) (string, error) {
	rows, err := deps.Repo.LookupFigmaSectionFileKey(ctx, sectionID)
	if err != nil {
		return "", err
	}
	return rows, nil
}
