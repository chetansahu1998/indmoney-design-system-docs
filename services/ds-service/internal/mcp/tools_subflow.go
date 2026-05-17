package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// tools_subflow.go — deep tools that wrap the sub_product / sub_flow repo
// methods shipped by U1. All three are Deep — the cold catalog reaches them
// via meta-verbs (drd.read, section.inspect) only.

// ─── shared response shapes ────────────────────────────────────────────────

type subflowSummary struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Slug           string  `json:"slug"`
	FullSlug       string  `json:"full_slug"`
	SubProductID   string  `json:"sub_product_id"`
	SubProductName string  `json:"sub_product_name,omitempty"`
	SubProductSlug string  `json:"sub_product_slug,omitempty"`
	HasDRD         bool    `json:"has_drd"`
	HasFigmaBound  bool    `json:"has_figma_section"`
	PrototypeURL   *string `json:"prototype_url,omitempty"`
	PrototypeTitle *string `json:"prototype_title,omitempty"`
}

type subflowSummaryProduct struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// summarize builds the response shape for a SubFlow paired with its parent
// SubProduct. Pass an empty SubProduct{} when the caller has not resolved
// it yet; the empty slug/name fields then drop out of the JSON via omitempty
// (the FullSlug fallback is the bare sub_flow slug).
func summarize(sf projects.SubFlow, sp projects.SubProduct) subflowSummary {
	s := subflowSummary{
		ID:             sf.ID,
		Name:           sf.Name,
		Slug:           sf.Slug,
		SubProductID:   sf.SubProductID,
		SubProductName: sp.Name,
		SubProductSlug: sp.Slug,
		HasDRD:         sf.DRDID != nil,
		HasFigmaBound:  sf.FigmaSectionID != nil,
		PrototypeURL:   sf.PrototypeURL,
		PrototypeTitle: sf.PrototypeTitle,
	}
	if sp.Slug != "" {
		s.FullSlug = sp.Slug + "/" + sf.Slug
	} else {
		s.FullSlug = sf.Slug
	}
	return s
}

// resolveSlug is the canonical helper for tools that take a sub_flow_slug
// argument. Returns the SubFlow row and its parent SubProduct (both
// tenant-scoped). Returns wrapped ErrInvalidArgs when slug is empty.
func resolveSlug(ctx context.Context, deps Deps, slug string) (projects.SubFlow, projects.SubProduct, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return projects.SubFlow{}, projects.SubProduct{},
			fmt.Errorf("%w: sub_flow_slug required", ErrInvalidArgs)
	}
	sf, err := deps.Repo.GetSubFlowBySlug(ctx, slug)
	if err != nil {
		return projects.SubFlow{}, projects.SubProduct{}, err
	}
	sp, err := deps.Repo.GetSubProductByID(ctx, sf.SubProductID)
	if err != nil {
		return sf, projects.SubProduct{}, fmt.Errorf("resolve sub_product %s: %w", sf.SubProductID, err)
	}
	return sf, sp, nil
}

// ─── subflow.list ──────────────────────────────────────────────────────────

type subflowListTool struct{}

type subflowListArgs struct {
	// SubProductFilter is the sub_product slug (e.g. "wallet") that scopes
	// the list. Empty string returns every sub_flow under the tenant.
	SubProductFilter string `json:"sub_product_filter,omitempty"`
}

func (subflowListTool) Name() string               { return "subflow.list" }
func (subflowListTool) Visibility() ToolVisibility { return Deep }
func (subflowListTool) Description() string {
	return "List sub_flows in the tenant. Optional sub_product_filter (slug) scopes the result."
}
func (subflowListTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_product_filter": {"type": "string", "description": "optional sub_product slug (e.g. \"wallet\"); empty = all"}
		},
		"additionalProperties": false
	}`)
}
func (subflowListTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in subflowListArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	in.SubProductFilter = strings.TrimSpace(in.SubProductFilter)

	var subProductID string
	if in.SubProductFilter != "" {
		sp, err := deps.Repo.GetSubProductBySlug(ctx, in.SubProductFilter)
		if err != nil {
			if errors.Is(err, projects.ErrNotFound) {
				// Unknown sub_product → empty list, not an error.
				return Result{Data: []subflowSummary{}}, nil
			}
			return Result{}, fmt.Errorf("subflow.list: %w", err)
		}
		subProductID = sp.ID
	}

	subflows, err := deps.Repo.ListSubFlows(ctx, subProductID)
	if err != nil {
		return Result{}, fmt.Errorf("subflow.list: %w", err)
	}

	// Resolve sub_product details once per unique id and cache.
	cache := map[string]projects.SubProduct{}
	out := make([]subflowSummary, 0, len(subflows))
	for _, sf := range subflows {
		sp, ok := cache[sf.SubProductID]
		if !ok {
			loaded, err := deps.Repo.GetSubProductByID(ctx, sf.SubProductID)
			if err != nil {
				return Result{}, fmt.Errorf("subflow.list: resolve sub_product %s: %w", sf.SubProductID, err)
			}
			cache[sf.SubProductID] = loaded
			sp = loaded
		}
		out = append(out, summarize(sf, sp))
	}
	return Result{Data: out}, nil
}

// ─── subflow.get ───────────────────────────────────────────────────────────

type subflowGetTool struct{}

type subflowGetArgs struct {
	Slug string `json:"slug"`
}

func (subflowGetTool) Name() string               { return "subflow.get" }
func (subflowGetTool) Visibility() ToolVisibility { return Deep }
func (subflowGetTool) Description() string {
	return "Get one sub_flow by its universal slug \"{sub_product_slug}/{sub_flow_slug}\"."
}
func (subflowGetTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {"slug": {"type": "string", "description": "e.g. \"wallet/m2m-settlement\""}},
		"required": ["slug"],
		"additionalProperties": false
	}`)
}
func (subflowGetTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in subflowGetArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	sf, sp, err := resolveSlug(ctx, deps, in.Slug)
	if err != nil {
		return Result{}, fmt.Errorf("subflow.get: %w", err)
	}
	return Result{Data: summarize(sf, sp)}, nil
}

// ─── subflow.create ─────────────────────────────────────────────────────────

type subflowCreateTool struct{}

type subflowCreateArgs struct {
	SubProduct     string `json:"sub_product"`
	SubFlow        string `json:"sub_flow"`
	PrototypeURL   string `json:"prototype_url,omitempty"`
	PrototypeTitle string `json:"prototype_title,omitempty"`
}

type subflowCreateResult struct {
	SubProduct subflowSummaryProduct `json:"sub_product"`
	SubFlow    subflowSummary        `json:"sub_flow"`
	Prototype  *prototypeSummary     `json:"prototype,omitempty"`
}

type prototypeSummary struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

func (subflowCreateTool) Name() string               { return "subflow.create" }
func (subflowCreateTool) Visibility() ToolVisibility { return Deep }
func (subflowCreateTool) Description() string {
	return "Create (or upsert) a sub_product + sub_flow pair. Optional prototype URL attaches an HTML placeholder until the designer ships."
}
func (subflowCreateTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"sub_product":      {"type": "string", "description": "e.g. \"Wallet\""},
			"sub_flow":         {"type": "string", "description": "e.g. \"M2M Settlement\""},
			"prototype_url":    {"type": "string", "description": "optional https:// URL of an interactive prototype"},
			"prototype_title":  {"type": "string", "description": "optional companion label for the prototype"}
		},
		"required": ["sub_product", "sub_flow"],
		"additionalProperties": false
	}`)
}
func (subflowCreateTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in subflowCreateArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	in.SubProduct = strings.TrimSpace(in.SubProduct)
	in.SubFlow = strings.TrimSpace(in.SubFlow)
	in.PrototypeURL = strings.TrimSpace(in.PrototypeURL)
	in.PrototypeTitle = strings.TrimSpace(in.PrototypeTitle)
	if in.SubProduct == "" {
		return Result{}, fmt.Errorf("%w: sub_product required", ErrInvalidArgs)
	}
	if in.SubFlow == "" {
		return Result{}, fmt.Errorf("%w: sub_flow required", ErrInvalidArgs)
	}
	sp, err := deps.Repo.UpsertSubProduct(ctx, in.SubProduct)
	if err != nil {
		return Result{}, fmt.Errorf("subflow.create: upsert sub_product: %w", err)
	}
	sf, err := deps.Repo.UpsertSubFlow(ctx, sp.ID, in.SubFlow)
	if err != nil {
		return Result{}, fmt.Errorf("subflow.create: upsert sub_flow: %w", err)
	}

	var proto *prototypeSummary
	if in.PrototypeURL != "" {
		if err := deps.Repo.AttachPrototype(ctx, sf.ID, in.PrototypeURL, in.PrototypeTitle, deps.Broker); err != nil {
			return Result{}, fmt.Errorf("subflow.create: attach prototype: %w", err)
		}
		proto = &prototypeSummary{URL: in.PrototypeURL, Title: in.PrototypeTitle}
		// Re-load so the response carries the prototype-* columns.
		reloaded, lerr := deps.Repo.GetSubFlowBySlug(ctx, sp.Slug+"/"+sf.Slug)
		if lerr == nil {
			sf = reloaded
		}
	}

	out := subflowCreateResult{
		SubProduct: subflowSummaryProduct{ID: sp.ID, Name: sp.Name, Slug: sp.Slug},
		SubFlow:    summarize(sf, sp),
		Prototype:  proto,
	}

	// Next-action hint — point at drd.read to seed the DRD.
	hints := []NextAction{
		{
			Tool: "drd.read",
			When: "to open (or seed) the DRD for this sub_flow",
			InputHint: rawJSON(`{"sub_flow_slug": "` + sp.Slug + `/` + sf.Slug + `"}`),
		},
	}
	return Result{Data: out, NextActions: hints}, nil
}
