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
//
// Audit thread-through (plan 2026-05-17-004 / U2): every write op records
// a `prd_audit` row via `recordAudit` after the underlying repo write
// succeeds. The call is best-effort — a failed audit is logged and
// swallowed; a successful PRD write must NOT be rolled back because of
// an audit insert failure. See KTD-2 of the plan and the contract
// comment on `projects.TenantRepo.RecordPRDAudit`.
//
// Read ops (`prd.get`, `prd.export`) record no audit. `prd.upsert_tab`
// also records no audit: tabs are structural (no `prd_state.id` to key
// the audit row on); `prd_audit` is per-state by design.

// ─── prd.get ───────────────────────────────────────────────────────────────

type prdGetTool struct{}

type prdGetArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

func (prdGetTool) Name() string               { return "prd.get" }
func (prdGetTool) Visibility() ToolVisibility { return Visible }
func (prdGetTool) Title() string              { return "Get PRD" }
func (prdGetTool) SideEffects() SideEffect    { return ReadOnly }
func (prdGetTool) DeferLoading() bool         { return false }
func (prdGetTool) Description() string {
	return "Load the full PRD tree for a sub_flow (tabs, states, all typed stems, frame tags). Use when you need the complete authored shape before mutating — e.g. to confirm a state exists or count criteria. Don't use when you only need a summary (call section.inspect — it carries DRD/PRD/frames counts in one round-trip). Returns prd:nil when no PRD has been seeded yet."
}
func (prdGetTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."}},
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
func (prdUpsertTabTool) Visibility() ToolVisibility { return Visible }
func (prdUpsertTabTool) Title() string              { return "Upsert PRD Tab" }
func (prdUpsertTabTool) SideEffects() SideEffect    { return Mutating }
func (prdUpsertTabTool) DeferLoading() bool         { return false }
func (prdUpsertTabTool) Description() string {
	return "Create or update a PRD tab keyed by (prd, name); auto-creates the parent PRD row if missing. Use when you need a new tab (e.g. \"Investment\", \"Reconciliation\") before adding states to it, or to refresh a tab's overview markdown. Don't use when you just want to add a state — prd.add_state creates the tab on demand via resolveTab. Idempotent on (prd, name). Tab writes do not emit prd_audit rows."
}
func (prdUpsertTabTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"name":          {"type": "string", "description": "Tab name (human label). Idempotency key together with the PRD id."},
			"position":      {"type": "integer", "description": "Ordering hint within the PRD; lower values render first."},
			"overview_md":   {"type": "string", "description": "Optional markdown blurb shown above the tab's states (problem, goals, etc.)."}
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
	// Audit: skipped. Tabs are structural — `prd_audit` is keyed on
	// prd_state.id (FK enforced) and there's no representative state at
	// tab creation time. The wall's `last_touched_*` lights up the moment
	// a state is added via prd.add_state, which is the load-bearing
	// authoring action PMs care about. Plan 2026-05-17-004 U2 KTD-2.
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
func (prdAddStateTool) Visibility() ToolVisibility { return Visible }
func (prdAddStateTool) Title() string              { return "Add PRD State" }
func (prdAddStateTool) SideEffects() SideEffect    { return Mutating }
func (prdAddStateTool) DeferLoading() bool         { return false }
func (prdAddStateTool) Description() string {
	return "Add (or update via idempotent restore) a PRD state inside a tab; tab + PRD auto-created. Use when the PM names a state (e.g. \"Cold state\", \"Loading\") and you want to record its condition / design / FE handling notes. Don't use when you want to bind a Figma frame — prd.attach_frame is the per-state binding. Idempotent on (tab, label); records prd_audit with op=upsert_state."
}
func (prdAddStateTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug":      {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":           {"type": "string", "description": "Parent tab name. Tab is auto-created if missing."},
			"label":              {"type": "string", "description": "State label (e.g. \"Cold state\"). Idempotency key together with tab_name."},
			"position":           {"type": "integer", "description": "Ordering hint within the tab; lower values render first."},
			"frame_name":         {"type": "string", "description": "Optional Figma frame name shorthand — recorded on the state for cross-reference."},
			"condition_md":       {"type": "string", "description": "Markdown describing when this state applies (the guard condition)."},
			"design_handling_md": {"type": "string", "description": "Markdown describing how the design responds in this state."},
			"fe_handling_md":     {"type": "string", "description": "Markdown describing how the frontend implementation handles this state."}
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
	recordAudit(ctx, deps, state.ID, projects.OpUpsertState)
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
func (prdAddEventTool) Visibility() ToolVisibility { return Visible }
func (prdAddEventTool) Title() string              { return "Add Mixpanel Event" }
func (prdAddEventTool) SideEffects() SideEffect    { return Mutating }
func (prdAddEventTool) DeferLoading() bool         { return false }
func (prdAddEventTool) Description() string {
	return "Declare a Mixpanel event tied to a PRD state (name, properties schema, fires_on guard). Use when the PM specifies analytics on a state — the event name becomes part of the resolver's mixpanel_event_names array. Don't use when the trigger is an acceptance criterion or copy string — use prd.add_acceptance_criterion or prd.upsert_copy_string respectively. Idempotent on (state, name); records prd_audit op=add_event."
}
func (prdAddEventTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug":     {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":          {"type": "string", "description": "Parent tab name; auto-created if missing."},
			"state_label":       {"type": "string", "description": "Parent state label; auto-created if missing."},
			"name":              {"type": "string", "description": "Mixpanel event name (e.g. \"wallet_cold_state_viewed\"). Idempotency key together with the state."},
			"position":          {"type": "integer", "description": "Ordering hint among the state's events; lower values render first."},
			"properties_schema": {"type": "string", "description": "Opaque JSON describing the event payload; the server does not parse or validate it."},
			"fires_on":          {"type": "string", "description": "Free-form guard text (e.g. \"on mount\", \"on CTA tap\") describing when the event fires."}
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
	recordAudit(ctx, deps, state.ID, projects.OpAddEvent)
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
	return Visible
}
func (prdAddAcceptanceCriterionTool) Title() string              { return "Add Acceptance Criterion" }
func (prdAddAcceptanceCriterionTool) SideEffects() SideEffect    { return Mutating }
func (prdAddAcceptanceCriterionTool) DeferLoading() bool         { return false }
func (prdAddAcceptanceCriterionTool) Description() string {
	return "Append an acceptance criterion (testable assertion) to a PRD state. Use when the PM articulates a must-be-true rule for a state — the QA / Playwright stub generator reads these. Don't use when the rule is an edge case (call prd.add_edge_case) or an accessibility rule (call prd.add_a11y_note); each stem has its own slot. Not deduped — repeated calls append additional rows; records prd_audit op=add_acceptance_criterion."
}
func (prdAddAcceptanceCriterionTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":      {"type": "string", "description": "Parent tab name; auto-created if missing."},
			"state_label":   {"type": "string", "description": "Parent state label; auto-created if missing."},
			"criterion":     {"type": "string", "description": "Single acceptance criterion (a testable assertion, written as one sentence)."},
			"position":      {"type": "integer", "description": "Ordering hint among the state's criteria; lower values render first."}
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
	recordAudit(ctx, deps, state.ID, projects.OpAddAcceptanceCriterion)
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
func (prdAddEdgeCaseTool) Visibility() ToolVisibility { return Visible }
func (prdAddEdgeCaseTool) Title() string              { return "Add Edge Case" }
func (prdAddEdgeCaseTool) SideEffects() SideEffect    { return Mutating }
func (prdAddEdgeCaseTool) DeferLoading() bool         { return false }
func (prdAddEdgeCaseTool) Description() string {
	return "Append an edge case (boundary / failure scenario) to a PRD state. Use when the PM names a specific corner — e.g. \"empty list\", \"network 500\", \"user has no KYC\". Don't use when the rule is a happy-path expectation (call prd.add_acceptance_criterion) or an accessibility concern (call prd.add_a11y_note). Not deduped — repeated calls append; records prd_audit op=add_edge_case."
}
func (prdAddEdgeCaseTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":      {"type": "string", "description": "Parent tab name; auto-created if missing."},
			"state_label":   {"type": "string", "description": "Parent state label; auto-created if missing."},
			"edge_case":     {"type": "string", "description": "Single edge case description (one boundary / failure scenario)."},
			"position":      {"type": "integer", "description": "Ordering hint among the state's edge cases; lower values render first."}
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
	recordAudit(ctx, deps, state.ID, projects.OpAddEdgeCase)
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
func (prdUpsertCopyStringTool) Visibility() ToolVisibility { return Visible }
func (prdUpsertCopyStringTool) Title() string              { return "Upsert Copy String" }
func (prdUpsertCopyStringTool) SideEffects() SideEffect    { return Mutating }
func (prdUpsertCopyStringTool) DeferLoading() bool         { return false }
func (prdUpsertCopyStringTool) Description() string {
	return "Upsert an i18n copy_string on a PRD state (the canonical user-facing text for a slot). Use when the PM dictates copy — title, body, CTA, error — for a specific state. Don't use when the text is a guard condition or design note (those go inside prd.add_state's *_md fields). Idempotent on (state, key, locale); records prd_audit op=upsert_copy_string."
}
func (prdUpsertCopyStringTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":      {"type": "string", "description": "Parent tab name; auto-created if missing."},
			"state_label":   {"type": "string", "description": "Parent state label; auto-created if missing."},
			"key":           {"type": "string", "description": "Copy slot identifier (e.g. \"title\", \"primary_cta\", \"empty.body\"). Idempotency key with locale."},
			"value":         {"type": "string", "description": "The localized text content for this key + locale pair."},
			"locale":        {"type": "string", "description": "BCP-47 / ISO locale tag (e.g. \"en\", \"hi\"). Defaults to \"en\" when empty."}
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
	recordAudit(ctx, deps, state.ID, projects.OpUpsertCopyString)
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
func (prdAddA11yNoteTool) Visibility() ToolVisibility { return Visible }
func (prdAddA11yNoteTool) Title() string              { return "Add A11y Note" }
func (prdAddA11yNoteTool) SideEffects() SideEffect    { return Mutating }
func (prdAddA11yNoteTool) DeferLoading() bool         { return false }
func (prdAddA11yNoteTool) Description() string {
	return "Append an accessibility note (aria, focus, screen-reader behaviour) to a PRD state. Use when the PM or designer specifies a11y handling — keyboard nav, label text, contrast notes, motion-sensitivity. Don't use when the note is a general acceptance criterion (call prd.add_acceptance_criterion) or a copy string (call prd.upsert_copy_string). Not deduped; records prd_audit op=add_a11y_note."
}
func (prdAddA11yNoteTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":      {"type": "string", "description": "Parent tab name; auto-created if missing."},
			"state_label":   {"type": "string", "description": "Parent state label; auto-created if missing."},
			"note":          {"type": "string", "description": "Single accessibility note (one a11y concern, written as one sentence)."},
			"position":      {"type": "integer", "description": "Ordering hint among the state's a11y notes; lower values render first."}
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
	recordAudit(ctx, deps, state.ID, projects.OpAddA11yNote)
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
func (prdAttachFrameTool) Visibility() ToolVisibility { return Visible }
func (prdAttachFrameTool) Title() string              { return "Attach Figma Frame" }
func (prdAttachFrameTool) SideEffects() SideEffect    { return Mutating }
func (prdAttachFrameTool) DeferLoading() bool         { return false }
func (prdAttachFrameTool) Description() string {
	return "Bind a Figma node id to a PRD state, with an optional platform variant (android | ios | desktop). Use when the PM picks the canonical frame for a state from the coverage wall — this is the load-bearing authoring action the wall's last_touched_* lights up on. Don't use when you only need a prototype URL for the whole sub_flow (call drd.attach_prototype). Records prd_audit op=attach_frame_tag."
}
func (prdAttachFrameTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."},
			"tab_name":      {"type": "string", "description": "Parent tab name; auto-created if missing."},
			"state_label":   {"type": "string", "description": "Parent state label; auto-created if missing."},
			"figma_node_id": {"type": "string", "description": "Figma node id (e.g. \"123:456\") of the frame to bind to this state."},
			"variant":       {"type": "string", "description": "Optional platform variant: android | ios | desktop. Empty means default / shared."},
			"position":      {"type": "integer", "description": "Ordering hint among the state's frame tags; lower values render first."}
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
	recordAudit(ctx, deps, state.ID, projects.OpAttachFrameTag)
	return Result{Data: tag}, nil
}

// ─── prd.detach_frame ──────────────────────────────────────────────────────

type prdDetachFrameTool struct{}

type prdDetachFrameArgs struct {
	TagID string `json:"tag_id"`
}

func (prdDetachFrameTool) Name() string               { return "prd.detach_frame" }
func (prdDetachFrameTool) Visibility() ToolVisibility { return Visible }
func (prdDetachFrameTool) Title() string              { return "Detach Figma Frame" }
func (prdDetachFrameTool) SideEffects() SideEffect    { return Destructive }
func (prdDetachFrameTool) DeferLoading() bool         { return false }
func (prdDetachFrameTool) Description() string {
	return "Remove one frame_tag row by its tag_id (destructive: the binding is deleted). Use when a frame was attached to the wrong state or the Figma node was retired. Don't use when you want to rebind to a different node — call prd.attach_frame instead (the new binding coexists or supersedes via position). No audit row is emitted (prd_audit keys on state_id, which is gone post-delete)."
}
func (prdDetachFrameTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"tag_id": {"type": "string", "description": "Server-issued frame_tag row id (returned by prd.attach_frame's Data.id)."}},
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
	// Audit: skipped. prd_audit is per-state by design (FK on
	// prd_state_id), and resolving tag_id → prd_state_id BEFORE delete
	// would require a new exported lookup on projects.TenantRepo —
	// out-of-scope per the plan's "instrumentation only" boundary
	// (U2: do not modify prd.go). Recording the audit AFTER delete is
	// impossible (the row is gone). Net effect: detach operations are
	// invisible to the coverage wall's last_touched_* columns. Acceptable:
	// the attach is the load-bearing authoring action that wall consumers
	// care about; detach is the cleanup tail. Revisit if PMs complain that
	// "I just unbound this frame" doesn't surface in the wall.
	return Result{Data: map[string]any{"tag_id": in.TagID, "detached": true}}, nil
}

// ─── prd.export ─────────────────────────────────────────────────────────────

type prdExportTool struct{}

type prdExportArgs struct {
	SubFlowSlug string `json:"sub_flow_slug"`
}

// prdExportResult is the wire shape returned by prd.export.
//
//   - Markdown: deterministic markdown for human reading (unchanged
//     from pre-U4).
//   - Sidecar:  the typed PRDFull tree — same shape as prd.get returns.
//     Downstream consumers (Storybook story generator, Playwright
//     stub generator, Mixpanel tracking-plan importer, JIRA story
//     creator) read the sidecar instead of re-parsing markdown.
//   - SubFlowFullSlug: `{sub_product_slug}/{sub_flow_slug}` join key
//     so the bridge can write `<sub_flow>.md` + `<sub_flow>.json`
//     under `~/INDmoney/<LOB>/Documents/` without re-resolving.
//   - Bytes:        markdown byte length (size budgeting; semantics
//     unchanged from pre-U4).
//   - SidecarBytes: serialized JSON byte length of the sidecar.
//
// Plan 2026-05-17-004 / U4. The sidecar IS PRDFull serialized — no
// separate type lives in this package.
type prdExportResult struct {
	SubFlowID       string            `json:"sub_flow_id"`
	SubFlowFullSlug string            `json:"sub_flow_full_slug"`
	Markdown        string            `json:"markdown"`
	Sidecar         projects.PRDFull  `json:"sidecar"`
	Bytes           int               `json:"bytes"`
	SidecarBytes    int               `json:"sidecar_bytes"`
}

func (prdExportTool) Name() string               { return "prd.export" }
func (prdExportTool) Visibility() ToolVisibility { return Visible }
func (prdExportTool) Title() string              { return "Export PRD as JSON" }
func (prdExportTool) SideEffects() SideEffect    { return ReadOnly }
func (prdExportTool) DeferLoading() bool         { return false }
func (prdExportTool) Description() string {
	return "Render the PRD as deterministic markdown plus a typed JSON sidecar (PRDFull shape). Use when the PM wants to publish docs, hand off to FE, or feed downstream stub generators (Storybook, Playwright, JIRA, tracking-plan). Don't use when you only want the typed tree for in-process consumption — call prd.get directly. Read-only; no filesystem write — the caller decides where the bytes land."
}
func (prdExportTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"sub_flow_slug": {"type": "string", "description": "Universal join key {sub_product_slug}/{sub_flow_slug}."}},
		"required": ["sub_flow_slug"],
		"additionalProperties": false
	}`)
}
func (prdExportTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in prdExportArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, sp, err := resolveSlug(ctx, deps, in.SubFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("prd.export: %w", err)
	}
	export, err := deps.Repo.RenderPRDExport(ctx, sf.ID)
	if err != nil {
		return Result{}, fmt.Errorf("prd.export: %w", err)
	}
	// Best-effort sidecar byte count — used by callers for size
	// budgeting. JSON marshal failures here should not block the
	// export; downstream callers receive sidecar_bytes=0 and can
	// recompute from Sidecar themselves.
	sidecarBytes := 0
	if b, mErr := json.Marshal(export.Sidecar); mErr == nil {
		sidecarBytes = len(b)
	}
	return Result{Data: prdExportResult{
		SubFlowID:       sf.ID,
		SubFlowFullSlug: sf.FullSlug(sp),
		Markdown:        export.Markdown,
		Sidecar:         export.Sidecar,
		Bytes:           len(export.Markdown),
		SidecarBytes:    sidecarBytes,
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

// recordAudit fires a best-effort prd_audit insert after a successful
// write. A failure here is logged via the per-request logger and
// swallowed — never returned. The contract (per plan KTD-2): a failed
// audit is strictly less bad than a refused edit.
//
// Callers pass the `prd_state.id` the write affected. For tools that
// write to a state-keyed stem (events, criteria, copy strings, etc.)
// the state id is already in hand from resolveState. For tools that
// have no state context (upsert_tab; detach_frame after the row is
// gone) the caller must skip — there's nothing to key the audit on.
func recordAudit(ctx context.Context, deps Deps, stateID string, op projects.PRDAuditOp) {
	if err := deps.Repo.RecordPRDAudit(ctx, stateID, deps.UserID, op); err != nil {
		toolLog(deps).Warn("prd_audit insert failed",
			"op", string(op),
			"state_id", stateID,
			"user_id", deps.UserID,
			"err", err.Error(),
		)
	}
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
