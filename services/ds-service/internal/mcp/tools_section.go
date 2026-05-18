package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
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
func (sectionFramesTool) Title() string              { return "List Section Frames" }
func (sectionFramesTool) SideEffects() SideEffect    { return ReadOnly }
func (sectionFramesTool) DeferLoading() bool         { return true }
func (sectionFramesTool) Description() string {
	return "List the direct-child Figma frames of a sub_flow's bound section (designer names, y-axis order, abs coords). Use when you only need the frame inventory — e.g. to populate a picker before prd.attach_frame. Don't use when you also want PRD coverage / binding status (call section.outline_states for the wall, or section.inspect for the full bundle). Returns empty frames + a note when no Figma section is bound."
}
func (sectionFramesTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."}},
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

// ─── section.outline_states (U6b) ──────────────────────────────────────────

type sectionOutlineStatesTool struct{}

type sectionOutlineStatesArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

// sectionOutlineStatesResult wraps projects.WallResult with the resolved
// sub_flow + section ids so a Wall caller doesn't need a second round-trip
// to know what was inspected.
type sectionOutlineStatesResult struct {
	SubFlowID      string              `json:"sub_flow_id"`
	FigmaSectionID *string             `json:"figma_section_id,omitempty"`
	Wall           projects.WallResult `json:"wall"`
	Note           string              `json:"note,omitempty"`
}

func (sectionOutlineStatesTool) Name() string               { return "section.outline_states" }
func (sectionOutlineStatesTool) Visibility() ToolVisibility { return Deep }
func (sectionOutlineStatesTool) Title() string              { return "Section Coverage Wall" }
func (sectionOutlineStatesTool) SideEffects() SideEffect    { return ReadOnly }
func (sectionOutlineStatesTool) DeferLoading() bool         { return true }
func (sectionOutlineStatesTool) Description() string {
	return "Coverage wall: every frame in the section joined with every PRD state, plus binding status, per-stem counts, total word count, and last-touched metadata. Use when the PM wants the resume-where-I-was view — \"which frames still need a state authored?\" Don't use when you only need the frame list (call section.frames) or only the PRD body (call prd.get). Read-only; populates orphans even when no Figma section is bound."
}
func (sectionOutlineStatesTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (sectionOutlineStatesTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in sectionOutlineStatesArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("section.outline_states: %w", err)
	}
	wall, err := deps.Repo.LoadSectionOutline(ctx, sf)
	if err != nil {
		return Result{}, fmt.Errorf("section.outline_states: %w", err)
	}
	out := sectionOutlineStatesResult{
		SubFlowID:      sf.ID,
		FigmaSectionID: sf.FigmaSectionID,
		Wall:           wall,
	}
	if sf.FigmaSectionID == nil || *sf.FigmaSectionID == "" {
		out.Note = "no figma section bound yet — wall shows orphans (if any) until autosync links the section"
	}

	// Next-action hint — point at the first untagged frame's add_state
	// call so resumption is one click away.
	hints := nextActionsForWall(wall, in.SubFlowSlug)
	return Result{Data: out, NextActions: hints}, nil
}

// nextActionsForWall emits the "where should I go next?" hint based on
// the current coverage. Priority order:
//  1. First untagged frame → suggest prd.add_state with the frame's name
//     pre-filled as label.
//  2. Else, first orphaned state → suggest reviewing the PRD.
//  3. Else (everything bound + authored) → suggest prd.export.
//
// Hints point at the promoted top-level prd.* tools (plan 002 U6) so the
// InputHint matches the deep tool's argument shape directly — no
// `{op, args}` envelope wrap.
func nextActionsForWall(wall projects.WallResult, slug string) []NextAction {
	for _, r := range wall.Frames {
		if r.BindingStatus == projects.BindingStatusUntagged {
			hint := rawJSON(`{"sub_flow_slug": "` + slug +
				`", "tab_name": "default", "label": "` + jsonEscape(r.FrameName) + `"}`)
			return []NextAction{{
				Tool:      "prd.add_state",
				When:      "first untagged frame — author this state next",
				InputHint: hint,
			}}
		}
	}
	for _, r := range wall.Frames {
		if r.BindingStatus == projects.BindingStatusOrphaned && r.PRDStateLabel != nil {
			return []NextAction{{
				Tool: "prd.get",
				When: "orphaned state — read PRD to decide restore vs purge",
				InputHint: rawJSON(`{"sub_flow_slug": "` +
					slug + `"}`),
			}}
		}
	}
	if wall.Counts.Bound > 0 {
		return []NextAction{{
			Tool: "prd.export",
			When: "coverage complete — render PRD as markdown",
			InputHint: rawJSON(`{"sub_flow_slug": "` +
				slug + `"}`),
		}}
	}
	return nil
}

// jsonEscape is the smallest viable escaper for the InputHint string
// fragments. Frame names rarely carry quotes / backslashes / control
// chars, but we still escape defensively so a stray `"` or `\` in a
// designer name doesn't break the next-action hint JSON.
//
// We do NOT use json.Marshal here because the surrounding template
// already embeds the value inside a larger JSON object — a full
// Marshal would add the surrounding quotes we don't want.
func jsonEscape(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if c < 0x20 {
				b = append(b, ' ')
			} else {
				b = append(b, c)
			}
		}
	}
	return string(b)
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
