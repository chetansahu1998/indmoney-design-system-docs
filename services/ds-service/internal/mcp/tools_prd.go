package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// tools_prd.go — eleven deep tools wrapping the PRD typed-stems repo (U4).
// All are Deep — the meta-verb `prd.author` (meta.go) dispatches to them
// via the `op` field. They're also directly callable for advanced clients.
//
// The shared pattern: resolve sub_flow_slug → SubFlow row → UpsertPRD
// (idempotent — re-runs return the existing row) → call the stem method.
// `tab_name` and `state_label` are name-keyed; we resolve them inside the
// tool to keep the wire shape PM-friendly (no UUIDs in args).

// ─── prd.get ───────────────────────────────────────────────────────────────

type prdGetTool struct{}

type prdGetArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

func (prdGetTool) Name() string               { return "prd.get" }
func (prdGetTool) Visibility() ToolVisibility { return Deep }
func (prdGetTool) Description() string {
	return "Load the full PRD (tabs, states, all stems, frame tags) for a sub_flow."
}
func (prdGetTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string"}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (prdGetTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdGetArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("prd.get: %w", err)
	}
	full, err := deps.Repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		if errors.Is(err, projects.ErrPRDNotFound) {
			return Result{Data: map[string]any{
				"sub_flow_id": sf.ID,
				"prd":         nil,
				"note":        "no PRD yet — use prd.author op=upsert_tab to seed one",
			}}, nil
		}
		return Result{}, fmt.Errorf("prd.get: %w", err)
	}
	return Result{Data: full}, nil
}

// ─── prd.upsert_tab ────────────────────────────────────────────────────────

type prdUpsertTabTool struct{}

type prdUpsertTabArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	Name        string `json:"name"`
	Position    int    `json:"position,omitempty"`
	OverviewMD  string `json:"overview_md,omitempty"`
}

func (prdUpsertTabTool) Name() string               { return "prd.upsert_tab" }
func (prdUpsertTabTool) Visibility() ToolVisibility { return Deep }
func (prdUpsertTabTool) Description() string {
	return "Create or update a PRD tab keyed by (prd, name). Auto-creates the parent PRD row if missing."
}
func (prdUpsertTabTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"name":          {"type": "string"},
			"position":      {"type": "integer"},
			"overview_md":   {"type": "string"}
		},
		"required": ["sub_flow_slug", "name"],
		"additionalProperties": false
	}`)
}
func (prdUpsertTabTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdUpsertTabArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, sp, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("prd.upsert_tab: %w", err)
	}
	prd, err := ensurePRD(ctx, deps, sf, sp)
	if err != nil {
		return Result{}, fmt.Errorf("prd.upsert_tab: %w", err)
	}
	tab, err := deps.Repo.UpsertPRDTab(ctx, projects.PRDTabInput{
		PRDID:      prd.ID,
		Name:       in.Name,
		Position:   in.Position,
		OverviewMD: in.OverviewMD,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.upsert_tab: %w", err)
	}
	return Result{Data: tab}, nil
}

// ─── prd.add_state ──────────────────────────────────────────────────────────

type prdAddStateTool struct{}

type prdAddStateArgs struct {
	SubFlowSlug      string `json:"sub_flow_slug"`
	TabName          string `json:"tab_name"`
	Label            string `json:"label"`
	Position         int    `json:"position,omitempty"`
	FrameName        string `json:"frame_name,omitempty"`
	ConditionMD      string `json:"condition_md,omitempty"`
	DesignHandlingMD string `json:"design_handling_md,omitempty"`
	FEHandlingMD     string `json:"fe_handling_md,omitempty"`
}

func (prdAddStateTool) Name() string               { return "prd.add_state" }
func (prdAddStateTool) Visibility() ToolVisibility { return Deep }
func (prdAddStateTool) Description() string {
	return "Add (or update via idempotent restore) a PRD state in a tab. Tab + PRD auto-created if missing."
}
func (prdAddStateTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug":      {"type": "string"},
			"tab_name":           {"type": "string"},
			"label":              {"type": "string"},
			"position":           {"type": "integer"},
			"frame_name":         {"type": "string"},
			"condition_md":       {"type": "string"},
			"design_handling_md": {"type": "string"},
			"fe_handling_md":     {"type": "string"}
		},
		"required": ["sub_flow_slug", "tab_name", "label"],
		"additionalProperties": false
	}`)
}
func (prdAddStateTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdAddStateArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	tab, _, _, err := resolveTab(ctx, deps, in.SubFlowSlug, in.TabName)
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_state: %w", err)
	}
	state, err := deps.Repo.UpsertPRDState(ctx, projects.PRDStateInput{
		PRDTabID:         tab.ID,
		Label:            in.Label,
		Position:         in.Position,
		FrameName:        in.FrameName,
		ConditionMD:      in.ConditionMD,
		DesignHandlingMD: in.DesignHandlingMD,
		FEHandlingMD:     in.FEHandlingMD,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_state: %w", err)
	}
	return Result{Data: state}, nil
}

// ─── prd.add_event ──────────────────────────────────────────────────────────

type prdAddEventTool struct{}

type prdAddEventArgs struct {
	SubFlowSlug      string `json:"sub_flow_slug"`
	TabName          string `json:"tab_name"`
	StateLabel       string `json:"state_label"`
	Name             string `json:"name"`
	Position         int    `json:"position,omitempty"`
	PropertiesSchema string `json:"properties_schema,omitempty"`
	FiresOn          string `json:"fires_on,omitempty"`
}

func (prdAddEventTool) Name() string               { return "prd.add_event" }
func (prdAddEventTool) Visibility() ToolVisibility { return Deep }
func (prdAddEventTool) Description() string {
	return "Add (or update idempotent on name) a Mixpanel event row tied to a PRD state."
}
func (prdAddEventTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug":     {"type": "string"},
			"tab_name":          {"type": "string"},
			"state_label":       {"type": "string"},
			"name":              {"type": "string"},
			"position":          {"type": "integer"},
			"properties_schema": {"type": "string", "description": "opaque JSON; the server does not parse it"},
			"fires_on":          {"type": "string"}
		},
		"required": ["sub_flow_slug", "tab_name", "state_label", "name"],
		"additionalProperties": false
	}`)
}
func (prdAddEventTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdAddEventArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	state, _, err := resolveState(ctx, deps, in.SubFlowSlug, in.TabName, in.StateLabel)
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_event: %w", err)
	}
	evt, err := deps.Repo.AddEvent(ctx, projects.EventInput{
		PRDStateID:       state.ID,
		Position:         in.Position,
		Name:             in.Name,
		PropertiesSchema: in.PropertiesSchema,
		FiresOn:          in.FiresOn,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_event: %w", err)
	}
	return Result{Data: evt}, nil
}

// ─── prd.add_acceptance_criterion ──────────────────────────────────────────

type prdAddAcceptanceCriterionTool struct{}

type prdAddAcceptanceCriterionArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	TabName     string `json:"tab_name"`
	StateLabel  string `json:"state_label"`
	Criterion   string `json:"criterion"`
	Position    int    `json:"position,omitempty"`
}

func (prdAddAcceptanceCriterionTool) Name() string { return "prd.add_acceptance_criterion" }
func (prdAddAcceptanceCriterionTool) Visibility() ToolVisibility {
	return Deep
}
func (prdAddAcceptanceCriterionTool) Description() string {
	return "Append an acceptance criterion to a PRD state."
}
func (prdAddAcceptanceCriterionTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"tab_name":      {"type": "string"},
			"state_label":   {"type": "string"},
			"criterion":     {"type": "string"},
			"position":      {"type": "integer"}
		},
		"required": ["sub_flow_slug", "tab_name", "state_label", "criterion"],
		"additionalProperties": false
	}`)
}
func (prdAddAcceptanceCriterionTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdAddAcceptanceCriterionArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	state, _, err := resolveState(ctx, deps, in.SubFlowSlug, in.TabName, in.StateLabel)
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_acceptance_criterion: %w", err)
	}
	row, err := deps.Repo.AddAcceptanceCriterion(ctx, projects.AcceptanceCriterionInput{
		PRDStateID: state.ID,
		Position:   in.Position,
		Criterion:  in.Criterion,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_acceptance_criterion: %w", err)
	}
	return Result{Data: row}, nil
}

// ─── prd.add_edge_case ──────────────────────────────────────────────────────

type prdAddEdgeCaseTool struct{}

type prdAddEdgeCaseArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	TabName     string `json:"tab_name"`
	StateLabel  string `json:"state_label"`
	EdgeCase    string `json:"edge_case"`
	Position    int    `json:"position,omitempty"`
}

func (prdAddEdgeCaseTool) Name() string               { return "prd.add_edge_case" }
func (prdAddEdgeCaseTool) Visibility() ToolVisibility { return Deep }
func (prdAddEdgeCaseTool) Description() string {
	return "Append an edge case to a PRD state."
}
func (prdAddEdgeCaseTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"tab_name":      {"type": "string"},
			"state_label":   {"type": "string"},
			"edge_case":     {"type": "string"},
			"position":      {"type": "integer"}
		},
		"required": ["sub_flow_slug", "tab_name", "state_label", "edge_case"],
		"additionalProperties": false
	}`)
}
func (prdAddEdgeCaseTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdAddEdgeCaseArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	state, _, err := resolveState(ctx, deps, in.SubFlowSlug, in.TabName, in.StateLabel)
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_edge_case: %w", err)
	}
	row, err := deps.Repo.AddEdgeCase(ctx, projects.EdgeCaseInput{
		PRDStateID: state.ID,
		Position:   in.Position,
		EdgeCase:   in.EdgeCase,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_edge_case: %w", err)
	}
	return Result{Data: row}, nil
}

// ─── prd.upsert_copy_string ────────────────────────────────────────────────

type prdUpsertCopyStringTool struct{}

type prdUpsertCopyStringArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	TabName     string `json:"tab_name"`
	StateLabel  string `json:"state_label"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Locale      string `json:"locale,omitempty"`
}

func (prdUpsertCopyStringTool) Name() string               { return "prd.upsert_copy_string" }
func (prdUpsertCopyStringTool) Visibility() ToolVisibility { return Deep }
func (prdUpsertCopyStringTool) Description() string {
	return "Upsert an i18n copy_string on a PRD state, idempotent on (key, locale)."
}
func (prdUpsertCopyStringTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"tab_name":      {"type": "string"},
			"state_label":   {"type": "string"},
			"key":           {"type": "string"},
			"value":         {"type": "string"},
			"locale":        {"type": "string", "description": "ISO locale tag; defaults to en"}
		},
		"required": ["sub_flow_slug", "tab_name", "state_label", "key", "value"],
		"additionalProperties": false
	}`)
}
func (prdUpsertCopyStringTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdUpsertCopyStringArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	state, _, err := resolveState(ctx, deps, in.SubFlowSlug, in.TabName, in.StateLabel)
	if err != nil {
		return Result{}, fmt.Errorf("prd.upsert_copy_string: %w", err)
	}
	row, err := deps.Repo.UpsertCopyString(ctx, projects.CopyStringInput{
		PRDStateID: state.ID,
		Key:        in.Key,
		Value:      in.Value,
		Locale:     in.Locale,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.upsert_copy_string: %w", err)
	}
	return Result{Data: row}, nil
}

// ─── prd.add_a11y_note ──────────────────────────────────────────────────────

type prdAddA11yNoteTool struct{}

type prdAddA11yNoteArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	TabName     string `json:"tab_name"`
	StateLabel  string `json:"state_label"`
	Note        string `json:"note"`
	Position    int    `json:"position,omitempty"`
}

func (prdAddA11yNoteTool) Name() string               { return "prd.add_a11y_note" }
func (prdAddA11yNoteTool) Visibility() ToolVisibility { return Deep }
func (prdAddA11yNoteTool) Description() string {
	return "Append an accessibility note to a PRD state."
}
func (prdAddA11yNoteTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"tab_name":      {"type": "string"},
			"state_label":   {"type": "string"},
			"note":          {"type": "string"},
			"position":      {"type": "integer"}
		},
		"required": ["sub_flow_slug", "tab_name", "state_label", "note"],
		"additionalProperties": false
	}`)
}
func (prdAddA11yNoteTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdAddA11yNoteArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	state, _, err := resolveState(ctx, deps, in.SubFlowSlug, in.TabName, in.StateLabel)
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_a11y_note: %w", err)
	}
	row, err := deps.Repo.AddA11yNote(ctx, projects.A11yNoteInput{
		PRDStateID: state.ID,
		Position:   in.Position,
		Note:       in.Note,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.add_a11y_note: %w", err)
	}
	return Result{Data: row}, nil
}

// ─── prd.attach_frame ──────────────────────────────────────────────────────

type prdAttachFrameTool struct{}

type prdAttachFrameArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
	TabName     string `json:"tab_name"`
	StateLabel  string `json:"state_label"`
	FigmaNodeID string `json:"figma_node_id"`
	Variant     string `json:"variant,omitempty"`
	Position    int    `json:"position,omitempty"`
}

func (prdAttachFrameTool) Name() string               { return "prd.attach_frame" }
func (prdAttachFrameTool) Visibility() ToolVisibility { return Deep }
func (prdAttachFrameTool) Description() string {
	return "Attach a Figma node to a PRD state (with optional platform variant)."
}
func (prdAttachFrameTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string"},
			"tab_name":      {"type": "string"},
			"state_label":   {"type": "string"},
			"figma_node_id": {"type": "string"},
			"variant":       {"type": "string", "description": "android | ios | desktop | …"},
			"position":      {"type": "integer"}
		},
		"required": ["sub_flow_slug", "tab_name", "state_label", "figma_node_id"],
		"additionalProperties": false
	}`)
}
func (prdAttachFrameTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdAttachFrameArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	state, _, err := resolveState(ctx, deps, in.SubFlowSlug, in.TabName, in.StateLabel)
	if err != nil {
		return Result{}, fmt.Errorf("prd.attach_frame: %w", err)
	}
	tag, err := deps.Repo.AttachFrameTag(ctx, projects.FrameTagInput{
		PRDStateID:  state.ID,
		FigmaNodeID: in.FigmaNodeID,
		Variant:     in.Variant,
		Position:    in.Position,
	})
	if err != nil {
		return Result{}, fmt.Errorf("prd.attach_frame: %w", err)
	}
	return Result{Data: tag}, nil
}

// ─── prd.detach_frame ──────────────────────────────────────────────────────

type prdDetachFrameTool struct{}

type prdDetachFrameArgs struct {
	TagID string `json:"tag_id"`
}

func (prdDetachFrameTool) Name() string               { return "prd.detach_frame" }
func (prdDetachFrameTool) Visibility() ToolVisibility { return Deep }
func (prdDetachFrameTool) Description() string {
	return "Detach a frame_tag by its tag_id."
}
func (prdDetachFrameTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"tag_id": {"type": "string"}},
		"required": ["tag_id"],
		"additionalProperties": false
	}`)
}
func (prdDetachFrameTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdDetachFrameArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(in.TagID) == "" {
		return Result{}, fmt.Errorf("%w: tag_id required", ErrInvalidArgs)
	}
	if err := deps.Repo.DetachFrameTag(ctx, in.TagID); err != nil {
		return Result{}, fmt.Errorf("prd.detach_frame: %w", err)
	}
	return Result{Data: map[string]any{"tag_id": in.TagID, "detached": true}}, nil
}

// ─── prd.export ─────────────────────────────────────────────────────────────

type prdExportTool struct{}

type prdExportArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

type prdExportResult struct {
	SubFlowID string `json:"sub_flow_id"`
	Markdown  string `json:"markdown"`
	Bytes     int    `json:"bytes"`
}

func (prdExportTool) Name() string               { return "prd.export" }
func (prdExportTool) Visibility() ToolVisibility { return Deep }
func (prdExportTool) Description() string {
	return "Render the PRD as deterministic markdown (no filesystem write — the caller decides where to put it)."
}
func (prdExportTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string"}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (prdExportTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdExportArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, _, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("prd.export: %w", err)
	}
	md, err := deps.Repo.RenderPRDMarkdown(ctx, sf.ID)
	if err != nil {
		return Result{}, fmt.Errorf("prd.export: %w", err)
	}
	return Result{Data: prdExportResult{
		SubFlowID: sf.ID,
		Markdown:  md,
		Bytes:     len(md),
	}}, nil
}

// ─── shared helpers ─────────────────────────────────────────────────────────

// ensurePRD returns the parent PRD row, creating one if it doesn't exist
// yet. Title defaults to "{SubProduct} — {SubFlow}".
func ensurePRD(ctx context.Context, deps Deps, sf projects.SubFlow, sp projects.SubProduct) (projects.PRD, error) {
	title := sf.Name
	if sp.Name != "" {
		title = sp.Name + " — " + sf.Name
	}
	return deps.Repo.UpsertPRD(ctx, projects.PRDInput{
		SubFlowID: sf.ID,
		Title:     title,
	})
}

// resolveTab returns (PRDTab, PRD, SubFlow) for a (slug, tabName) pair.
// PRD + tab are auto-created if missing (idempotent upserts). Empty
// tabName falls back to the conventional "default" sentinel (mirrors
// U2b's auto-skeleton tab name).
func resolveTab(ctx context.Context, deps Deps, slug, tabName string) (projects.PRDTab, projects.PRD, projects.SubFlow, error) {
	sf, sp, err := resolveSlug(ctx, deps, slug)
	if err != nil {
		return projects.PRDTab{}, projects.PRD{}, projects.SubFlow{}, err
	}
	if strings.TrimSpace(tabName) == "" {
		return projects.PRDTab{}, projects.PRD{}, sf,
			fmt.Errorf("%w: tab_name required", ErrInvalidArgs)
	}
	prd, err := ensurePRD(ctx, deps, sf, sp)
	if err != nil {
		return projects.PRDTab{}, projects.PRD{}, sf, fmt.Errorf("ensure prd: %w", err)
	}
	tab, err := deps.Repo.UpsertPRDTab(ctx, projects.PRDTabInput{
		PRDID: prd.ID,
		Name:  tabName,
	})
	if err != nil {
		return projects.PRDTab{}, prd, sf, fmt.Errorf("upsert tab %q: %w", tabName, err)
	}
	return tab, prd, sf, nil
}

// resolveState returns (PRDState, PRDTab) for a (slug, tabName, stateLabel)
// triple. The state is auto-created if missing (idempotent upsert).
func resolveState(ctx context.Context, deps Deps, slug, tabName, label string) (projects.PRDState, projects.PRDTab, error) {
	tab, _, _, err := resolveTab(ctx, deps, slug, tabName)
	if err != nil {
		return projects.PRDState{}, projects.PRDTab{}, err
	}
	if strings.TrimSpace(label) == "" {
		return projects.PRDState{}, tab, fmt.Errorf("%w: state_label required", ErrInvalidArgs)
	}
	state, err := deps.Repo.UpsertPRDState(ctx, projects.PRDStateInput{
		PRDTabID: tab.ID,
		Label:    label,
	})
	if err != nil {
		return projects.PRDState{}, tab, fmt.Errorf("upsert state %q: %w", label, err)
	}
	return state, tab, nil
}
