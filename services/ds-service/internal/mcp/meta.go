package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// meta.go — the 3 visible meta-verbs that make up the cold catalog.
// Each one composes deep-tool responses + appends `next_actions` to walk
// Claude through the PM workflow.
//
// Sizing budget (KTD-5): the three Description() strings + their
// InputSchema() JSON together should stay under ~1.5k bytes serialized.
// registry_test.go enforces this with a smoke-test snapshot.

// ─── drd.read ──────────────────────────────────────────────────────────────

type drdReadTool struct{}

type drdReadArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

type drdReadResult struct {
	SubFlow drdReadSubFlow `json:"sub_flow"`
	DRD     *drdReadDRD    `json:"drd"` // nil when no snapshot yet
}

type drdReadSubFlow struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	FullSlug        string  `json:"full_slug"`
	CanvasLifecycle string  `json:"canvas_lifecycle"`
	PrototypeURL    *string `json:"prototype_url,omitempty"`
	HasFigmaSection bool    `json:"has_figma_section"`
}

type drdReadDRD struct {
	YDocStateBase64 string `json:"y_doc_state_base64"`
	Bytes           int    `json:"bytes"`
}

func (drdReadTool) Name() string               { return "drd.read" }
func (drdReadTool) Visibility() ToolVisibility { return Visible }
func (drdReadTool) Description() string {
	return "Read a sub_flow's DRD content. Returns the YDoc state (base64) plus canvas lifecycle hints. Use this first when a PM names a sub_flow."
}
func (drdReadTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "{sub_product_slug}/{sub_flow_slug}"}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (drdReadTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in drdReadArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, sp, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("drd.read: %w", err)
	}
	lifecycle, err := deps.Repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		return Result{}, fmt.Errorf("drd.read: lifecycle: %w", err)
	}
	out := drdReadResult{
		SubFlow: drdReadSubFlow{
			ID:              sf.ID,
			Name:            sf.Name,
			Slug:            sf.Slug,
			FullSlug:        sp.Slug + "/" + sf.Slug,
			CanvasLifecycle: string(lifecycle),
			PrototypeURL:    sf.PrototypeURL,
			HasFigmaSection: sf.FigmaSectionID != nil,
		},
	}

	state, err := deps.Repo.LoadYDocStateBySubFlow(ctx, sf.ID)
	if err == nil && len(state) > 0 {
		out.DRD = &drdReadDRD{
			YDocStateBase64: base64.StdEncoding.EncodeToString(state),
			Bytes:           len(state),
		}
	} else if err != nil && !errors.Is(err, projects.ErrNotFound) {
		return Result{}, fmt.Errorf("drd.read: load ydoc: %w", err)
	}

	// Next actions — vary by lifecycle.
	hints := []NextAction{}
	if out.DRD == nil {
		hints = append(hints, NextAction{
			Tool: "drd.append",
			When: "to seed the DRD with initial content",
			InputHint: rawJSON(`{"sub_flow_slug": "` + out.SubFlow.FullSlug +
				`", "content_bytes_base64": "<base64 YDoc state>"}`),
		})
	}
	if lifecycle == projects.LifecycleDesignShipped {
		hints = append(hints, NextAction{
			Tool: "prd.author",
			Op:   "get",
			When: "design has shipped — read the current PRD with auto-skeleton states",
			InputHint: rawJSON(`{"op": "get", "args": {"sub_flow_slug": "` + out.SubFlow.FullSlug + `"}}`),
		})
		hints = append(hints, NextAction{
			Tool: "section.inspect",
			When: "to open the coverage wall for the bound section",
			InputHint: rawJSON(`{"sub_flow_slug": "` + out.SubFlow.FullSlug + `"}`),
		})
	} else {
		// Pre-ship — point at section.inspect for the prototype + DRD summary.
		hints = append(hints, NextAction{
			Tool: "section.inspect",
			When: "to view DRD + prototype + (when ready) frame coverage",
			InputHint: rawJSON(`{"sub_flow_slug": "` + out.SubFlow.FullSlug + `"}`),
		})
	}
	return Result{Data: out, NextActions: hints}, nil
}

// ─── prd.author ────────────────────────────────────────────────────────────

type prdAuthorTool struct{}

type prdAuthorArgs struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

// prdAuthorValidOps is the closed set of `op` values the meta-verb accepts.
// Each maps to a deep tool's Name(). The list is the wire contract — error
// messages enumerate it so Claude can correct itself without a docs round-trip.
var prdAuthorValidOps = []string{
	"get",
	"upsert_tab",
	"add_state",
	"add_event",
	"add_acceptance_criterion",
	"add_edge_case",
	"upsert_copy_string",
	"add_a11y_note",
	"attach_frame",
	"detach_frame",
	"export",
}

// prdAuthorOpToTool maps op → deep tool name. Kept beside the slice so a
// new op only needs to be added in one place.
var prdAuthorOpToTool = map[string]string{
	"get":                      "prd.get",
	"upsert_tab":               "prd.upsert_tab",
	"add_state":                "prd.add_state",
	"add_event":                "prd.add_event",
	"add_acceptance_criterion": "prd.add_acceptance_criterion",
	"add_edge_case":            "prd.add_edge_case",
	"upsert_copy_string":       "prd.upsert_copy_string",
	"add_a11y_note":            "prd.add_a11y_note",
	"attach_frame":             "prd.attach_frame",
	"detach_frame":             "prd.detach_frame",
	"export":                   "prd.export",
}

func (prdAuthorTool) Name() string               { return "prd.author" }
func (prdAuthorTool) Visibility() ToolVisibility { return Visible }
func (prdAuthorTool) Description() string {
	return "Author or read a PRD. Op-dispatched: {op: get|upsert_tab|add_state|add_event|add_acceptance_criterion|add_edge_case|upsert_copy_string|add_a11y_note|attach_frame|detach_frame|export, args: {...}}. Schema for args is op-specific; on first invocation, send op:get for the current shape."
}
func (prdAuthorTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"op":   {"type": "string", "description": "one of: get, upsert_tab, add_state, add_event, add_acceptance_criterion, add_edge_case, upsert_copy_string, add_a11y_note, attach_frame, detach_frame, export"},
			"args": {"type": "object", "description": "op-specific; the response's schema_hint carries the next op's shape"}
		},
		"required": ["op"],
		"additionalProperties": false
	}`)
}
func (p prdAuthorTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var meta prdAuthorArgs
	if err := decodeArgs(args, &meta); err != nil {
		return Result{}, err
	}
	op := strings.TrimSpace(meta.Op)
	if op == "" {
		return Result{}, fmt.Errorf("%w: op required (one of %v)", ErrInvalidArgs, prdAuthorValidOps)
	}
	toolName, ok := prdAuthorOpToTool[op]
	if !ok {
		return Result{}, fmt.Errorf("%w: op %q not valid; valid ops: %v",
			ErrInvalidArgs, op, prdAuthorValidOps)
	}
	// Pull the tool from the registry held by the meta-verb. We don't have
	// a registry pointer at the tool level, so the dispatch happens via
	// the shared package registry singleton populated in NewDefaultRegistry.
	// This keeps each prd.author call to one lookup + one Invoke.
	deepTool, ok := metaRegistry.Lookup(toolName)
	if !ok {
		return Result{}, fmt.Errorf("internal: deep tool %q missing from registry", toolName)
	}
	res, err := deepTool.Invoke(ctx, deps, meta.Args)
	if err != nil {
		return Result{}, err
	}
	// Append a workflow hint: after add_state, suggest add_event /
	// add_acceptance_criterion as the natural next moves.
	hints := nextActionsForPRDOp(op, meta.Args)
	if len(hints) > 0 {
		res.NextActions = append(res.NextActions, hints...)
	}
	return res, nil
}

// nextActionsForPRDOp emits workflow hints based on which op was just run.
// The hints encode the typical "after writing a state, you usually add an
// event, an acceptance criterion, and the design-handling markdown" path
// captured in the plan body.
func nextActionsForPRDOp(op string, raw json.RawMessage) []NextAction {
	switch op {
	case "get":
		return []NextAction{
			{Tool: "prd.author", Op: "add_state",
				When: "to add (or refine) a state in a tab"},
			{Tool: "prd.author", Op: "export",
				When: "to render the PRD as markdown"},
		}
	case "upsert_tab":
		return []NextAction{
			{Tool: "prd.author", Op: "add_state",
				When: "to populate the tab with states", InputHint: passthroughSlug(raw)},
		}
	case "add_state":
		return []NextAction{
			{Tool: "prd.author", Op: "add_acceptance_criterion",
				When: "to spell out what the state must satisfy", InputHint: passthroughSlug(raw)},
			{Tool: "prd.author", Op: "add_event",
				When: "to declare a Mixpanel event on the state", InputHint: passthroughSlug(raw)},
			{Tool: "prd.author", Op: "attach_frame",
				When: "to bind a Figma node to the state", InputHint: passthroughSlug(raw)},
		}
	case "add_event":
		return []NextAction{
			{Tool: "prd.author", Op: "add_acceptance_criterion",
				When: "to spell out what the event guarantees", InputHint: passthroughSlug(raw)},
		}
	}
	return nil
}

// passthroughSlug pulls sub_flow_slug + tab_name + state_label out of the
// raw op args (if present) so the next-action hint is pre-filled. Best
// effort — emits an empty hint if the raw bytes don't decode.
func passthroughSlug(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	hint := map[string]any{}
	for _, k := range []string{"sub_flow_slug", "tab_name", "state_label"} {
		if v, ok := m[k]; ok {
			hint[k] = v
		}
	}
	if len(hint) == 0 {
		return nil
	}
	b, err := json.Marshal(hint)
	if err != nil {
		return nil
	}
	return b
}

// ─── section.inspect ───────────────────────────────────────────────────────

type sectionInspectTool struct{}

type sectionInspectArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

type sectionInspectResult struct {
	SubFlow    drdReadSubFlow      `json:"sub_flow"`
	DRDSummary sectionDRDSummary   `json:"drd_summary"`
	PRDSummary sectionPRDSummary   `json:"prd_summary"`
	Frames     []sectionFrameRow   `json:"frames"`
	FramesNote string              `json:"frames_note,omitempty"`
}

type sectionDRDSummary struct {
	Exists bool `json:"exists"`
	Bytes  int  `json:"bytes,omitempty"`
}

type sectionPRDSummary struct {
	Exists     bool `json:"exists"`
	TabCount   int  `json:"tab_count"`
	StateCount int  `json:"state_count"`
}

func (sectionInspectTool) Name() string               { return "section.inspect" }
func (sectionInspectTool) Visibility() ToolVisibility { return Visible }
func (sectionInspectTool) Description() string {
	return "Inspect a sub_flow: returns sub_flow metadata, DRD/PRD existence summary, and the frames in the bound Figma section. The PM's default first call."
}
func (sectionInspectTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string"}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (sectionInspectTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in sectionInspectArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, sp, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("section.inspect: %w", err)
	}
	lifecycle, err := deps.Repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		return Result{}, fmt.Errorf("section.inspect: lifecycle: %w", err)
	}

	out := sectionInspectResult{
		SubFlow: drdReadSubFlow{
			ID:              sf.ID,
			Name:            sf.Name,
			Slug:            sf.Slug,
			FullSlug:        sp.Slug + "/" + sf.Slug,
			CanvasLifecycle: string(lifecycle),
			PrototypeURL:    sf.PrototypeURL,
			HasFigmaSection: sf.FigmaSectionID != nil,
		},
		Frames: []sectionFrameRow{},
	}

	// DRD summary — existence + size.
	state, err := deps.Repo.LoadYDocStateBySubFlow(ctx, sf.ID)
	if err == nil {
		out.DRDSummary.Exists = true
		out.DRDSummary.Bytes = len(state)
	} else if !errors.Is(err, projects.ErrNotFound) {
		return Result{}, fmt.Errorf("section.inspect: drd: %w", err)
	}

	// PRD summary — load via the full read; count tabs + (live) states.
	prd, err := deps.Repo.LoadPRD(ctx, sf.ID)
	if err == nil {
		out.PRDSummary.Exists = true
		out.PRDSummary.TabCount = len(prd.Tabs)
		for _, tab := range prd.Tabs {
			out.PRDSummary.StateCount += len(tab.States)
		}
	} else if !errors.Is(err, projects.ErrPRDNotFound) {
		return Result{}, fmt.Errorf("section.inspect: prd: %w", err)
	}

	// Frames — only when a section is bound.
	if sf.FigmaSectionID != nil && *sf.FigmaSectionID != "" {
		fileKey, err := deps.Repo.LookupFigmaSectionFileKey(ctx, *sf.FigmaSectionID)
		if err == nil {
			frames, err := deps.Repo.ListSectionFrames(ctx, fileKey, *sf.FigmaSectionID)
			if err != nil {
				return Result{}, fmt.Errorf("section.inspect: frames: %w", err)
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
		} else if errors.Is(err, projects.ErrNotFound) {
			out.FramesNote = "bound figma_section_id is dangling — try a re-sync"
		} else {
			return Result{}, fmt.Errorf("section.inspect: file_key: %w", err)
		}
	} else {
		out.FramesNote = "no figma section bound yet — use the prototype or wait for design"
	}

	// Next-action hints — point at the most useful next call.
	hints := []NextAction{
		{Tool: "drd.read",
			When: "to read the DRD content",
			InputHint: rawJSON(`{"sub_flow_slug": "` + out.SubFlow.FullSlug + `"}`),
		},
		{Tool: "prd.author", Op: "get",
			When: "to read the current PRD with auto-skeleton states",
			InputHint: rawJSON(`{"op": "get", "args": {"sub_flow_slug": "` + out.SubFlow.FullSlug + `"}}`),
		},
		{Tool: "prd.author", Op: "add_state",
			When: "to add a state to the PRD (or refine an auto-skeleton row)",
			InputHint: rawJSON(`{"op": "add_state", "args": {"sub_flow_slug": "` + out.SubFlow.FullSlug + `", "tab_name": "default", "label": "<State label>"}}`),
		},
	}
	return Result{Data: out, NextActions: hints}, nil
}

// ─── shared registry singleton ─────────────────────────────────────────────

// metaRegistry is the per-process registry that prd.author dispatches into.
// It's the same singleton NewDefaultRegistry returns; we hold a package-
// scoped reference so the meta-verb doesn't need to thread it through
// every Invoke call (the Deps shape is otherwise transport-agnostic).
//
// Test entry point: NewTestRegistry resets this to the freshly-built
// registry so per-test isolation works.
var metaRegistry *Registry

// NewDefaultRegistry constructs the registry with every tool wired in.
// Called once by main.go and by registry_test.go.
//
// Idempotent for the package-scoped metaRegistry: a second call overwrites
// the singleton (tests rebuild fixtures between cases).
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	// Visible (3 meta-verbs).
	r.Register(drdReadTool{})
	r.Register(prdAuthorTool{})
	r.Register(sectionInspectTool{})
	// Deep — subflow.*
	r.Register(subflowListTool{})
	r.Register(subflowGetTool{})
	r.Register(subflowCreateTool{})
	// Deep — section.*
	r.Register(sectionFramesTool{})
	r.Register(sectionOutlineStatesTool{})
	// Deep — drd.*
	r.Register(drdAppendTool{})
	r.Register(drdAttachPrototypeTool{})
	r.Register(drdDetachPrototypeTool{})
	// Deep — prd.*
	r.Register(prdGetTool{})
	r.Register(prdUpsertTabTool{})
	r.Register(prdAddStateTool{})
	r.Register(prdAddEventTool{})
	r.Register(prdAddAcceptanceCriterionTool{})
	r.Register(prdAddEdgeCaseTool{})
	r.Register(prdUpsertCopyStringTool{})
	r.Register(prdAddA11yNoteTool{})
	r.Register(prdAttachFrameTool{})
	r.Register(prdDetachFrameTool{})
	r.Register(prdExportTool{})

	metaRegistry = r
	return r
}
