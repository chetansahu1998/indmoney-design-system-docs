package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// tools_resolve.go — U9b of docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md.
//
// The `resolve` tool locks `{sub_product.slug}/{sub_flow.slug}` as the
// universal join key (KTD-6). It takes a 2-segment slug
// ("wallet/m2m-settlement") or a 3-segment slug
// ("wallet/m2m-settlement/cold-state") and returns the joined view —
// sub_flow + Figma frames + PRD states + downstream stubs.
//
// Stubs (MixpanelEventNames is populated from PRD declarations; the rest
// are intentionally empty `[]` in v1) advertise the contract: as
// downstream teams (analytics, Storybook, Sentry, JIRA) adopt the
// {sub_product}/{sub_flow}/{state} convention, the resolver becomes the
// org-wide read-side index. The point of shipping it now is the SHAPE.
//
// Visibility: Deep. The cold catalog (3 visible meta-verbs) stays
// stable; resolve is reachable via meta-verb next_actions or direct
// invocation by callers who already know the slug.
//
// Mirror: internal/auditbyslug/handler.go (slug-based GET, JWT-claim
// tenant resolution via *TenantRepo, returns enriched data). Same
// access pattern; the multi-source fan-out is new but small.

// ─── Result shape ──────────────────────────────────────────────────────────

// ResolveLevel discriminates entity-level ("sub_flow") from state-level
// ("state") results. Wire constants — downstream consumers branch on these.
const (
	ResolveLevelSubFlow = "sub_flow"
	ResolveLevelState   = "state"
)

// ResolveResult is the wire shape `resolve` returns. JSON tags MUST stay
// stable — every future system that joins on the slug keys off this.
type ResolveResult struct {
	Slug    string         `json:"slug"`
	Level   string         `json:"level"`             // "sub_flow" | "state"
	SubFlow ResolveSubFlow `json:"sub_flow"`
	State   *ResolveState  `json:"state,omitempty"`   // populated when level == "state"

	PRDExists  bool `json:"prd_exists"`
	FrameCount int  `json:"frame_count"`
	StateCount int  `json:"state_count"`

	// Declared things — populated from current data.
	MixpanelEventNames []string `json:"mixpanel_event_names"` // from prd_state_event.name, sorted, deduped
	DRDExists          bool     `json:"drd_exists"`
	PrototypeURL       string   `json:"prototype_url,omitempty"`
	CanvasLifecycle    string   `json:"canvas_lifecycle"`

	// Stubs — v1 returns empty arrays; downstream teams adopt the
	// convention. The shape is the contract.
	RecentEvents     []ResolveEventOccurrence `json:"recent_events"`
	OpenSentryIssues []ResolveSentryIssue     `json:"open_sentry_issues"`
	StorybookPaths   []string                 `json:"storybook_paths"`
	JIRAComponents   []string                 `json:"jira_components"`

	Links ResolveLinks `json:"links"`
}

// ResolveSubFlow is the sub_flow header, including the resolved
// sub_product name + slug. FullSlug is the canonical join key.
type ResolveSubFlow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`        // sub_flow.slug only
	SubProduct string `json:"sub_product"` // sub_product.name (display)
	FullSlug   string `json:"full_slug"`   // {sub_product.slug}/{sub_flow.slug}
}

// ResolveState is the narrowed view when the caller supplied a 3-segment
// slug that matched a live PRD state.
type ResolveState struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Position  int    `json:"position"`
	FrameName string `json:"frame_name,omitempty"`
}

// ResolveEventOccurrence is the placeholder shape for a Mixpanel event
// observation. v1 emits zero rows; the struct is documented so the
// analytics team can adopt the convention without renegotiating the
// wire shape.
type ResolveEventOccurrence struct {
	Name      string `json:"name"`
	Count     int    `json:"count"`
	WindowH   int    `json:"window_hours"`
	UpdatedAt string `json:"updated_at,omitempty"` // RFC3339
}

// ResolveSentryIssue is the placeholder shape for an open Sentry issue
// tagged with the sub_flow slug.
type ResolveSentryIssue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Level     string `json:"level"` // "error" | "warning" | "info"
	URL       string `json:"url"`
	UpdatedAt string `json:"updated_at,omitempty"` // RFC3339
}

// ResolveLinks bundles the slug-derived URLs a caller will commonly want.
type ResolveLinks struct {
	PRDViewerURL      string `json:"prd_viewer_url,omitempty"`
	FigmaURL          string `json:"figma_url,omitempty"`
	ConventionsDocURL string `json:"conventions_doc_url"`
}

// conventionsDocURL is the well-known path to the slug contract document.
// Lives in the docs/ folder of this repo and ships beside the resolver.
const conventionsDocURL = "/docs/conventions/sub-product-slug"

// ─── Tool ──────────────────────────────────────────────────────────────────

type resolveTool struct{}

type resolveArgs struct {
	Slug string `json:"slug"`
}

func (resolveTool) Name() string               { return "resolve" }
func (resolveTool) Visibility() ToolVisibility { return Deep }
func (resolveTool) Description() string {
	return "Resolve a universal sub-product slug ({sub_product}/{sub_flow} or " +
		"{sub_product}/{sub_flow}/{state}) to its joined view: sub_flow + " +
		"figma frames + PRD states + downstream stubs (mixpanel events, " +
		"storybook stories, sentry issues, jira components). The slug is " +
		"the org-wide identifier (KTD-6 of plan 002)."
}
func (resolveTool) InputSchema() json.RawMessage {
	return rawJSON(`{
		"type": "object",
		"properties": {
			"slug": {"type": "string", "description": "{sub_product}/{sub_flow} or {sub_product}/{sub_flow}/{state}; lowercase kebab-case"}
		},
		"required": ["slug"],
		"additionalProperties": false
	}`)
}

func (resolveTool) Invoke(ctx context.Context, deps Deps, args json.RawMessage) (Result, error) {
	var in resolveArgs
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}

	raw := strings.TrimSpace(in.Slug)
	if raw == "" {
		return Result{}, fmt.Errorf("%w: slug required", ErrInvalidArgs)
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 && len(parts) != 3 {
		return Result{}, fmt.Errorf(
			"%w: slug must be {sub_product}/{sub_flow} or {sub_product}/{sub_flow}/{state}, got %d segments",
			ErrInvalidArgs, len(parts))
	}
	for i, seg := range parts {
		if strings.TrimSpace(seg) == "" {
			return Result{}, fmt.Errorf("%w: slug segment %d is empty", ErrInvalidArgs, i+1)
		}
	}

	subFlowSlug := parts[0] + "/" + parts[1]

	// 1. Resolve sub_flow + sub_product.
	sf, sp, err := resolveSlug(ctx, deps, subFlowSlug)
	if err != nil {
		return Result{}, fmt.Errorf("resolve: %w", err)
	}

	// 2. Canvas lifecycle.
	lifecycle, err := deps.Repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		return Result{}, fmt.Errorf("resolve: lifecycle: %w", err)
	}

	// 3. PRD (tolerate missing).
	var (
		prdExists  bool
		stateCount int
		eventNames []string
		states     []projects.PRDStateFull
	)
	prd, perr := deps.Repo.LoadPRD(ctx, sf.ID)
	if perr == nil {
		prdExists = true
		eventSet := map[string]struct{}{}
		for _, tab := range prd.Tabs {
			for _, st := range tab.States {
				stateCount++
				states = append(states, st)
				for _, ev := range st.Events {
					name := strings.TrimSpace(ev.Name)
					if name == "" {
						continue
					}
					eventSet[name] = struct{}{}
				}
			}
		}
		eventNames = make([]string, 0, len(eventSet))
		for n := range eventSet {
			eventNames = append(eventNames, n)
		}
		sort.Strings(eventNames)
	} else if !errors.Is(perr, projects.ErrPRDNotFound) {
		return Result{}, fmt.Errorf("resolve: load prd: %w", perr)
	}
	if eventNames == nil {
		// Never return nil — the array shape is part of the contract.
		eventNames = []string{}
	}

	// 4. Frames (only when a Figma section is bound).
	frameCount := 0
	figmaURL := ""
	if sf.FigmaSectionID != nil && *sf.FigmaSectionID != "" {
		fileKey, lerr := deps.Repo.LookupFigmaSectionFileKey(ctx, *sf.FigmaSectionID)
		if lerr == nil {
			frames, ferr := deps.Repo.ListSectionFrames(ctx, fileKey, *sf.FigmaSectionID)
			if ferr != nil {
				return Result{}, fmt.Errorf("resolve: list frames: %w", ferr)
			}
			frameCount = len(frames)
			// Compose a Figma deeplink. The `node-id` query param wants the
			// section id with colons replaced by hyphens, per Figma's URL
			// convention. (Best-effort — viewer can rebuild from file_key.)
			if fileKey != "" {
				nodeQuery := strings.ReplaceAll(*sf.FigmaSectionID, ":", "-")
				figmaURL = "https://www.figma.com/file/" + fileKey + "/?node-id=" + nodeQuery
			}
		} else if !errors.Is(lerr, projects.ErrNotFound) {
			return Result{}, fmt.Errorf("resolve: lookup file_key: %w", lerr)
		}
	}

	// 5. PrototypeURL — from sub_flow. May be empty.
	prototypeURL := ""
	if sf.PrototypeURL != nil {
		prototypeURL = *sf.PrototypeURL
	}

	// 5b. DRD existence — match section.inspect's contract: a DRD "exists"
	//     when LoadYDocStateBySubFlow returns a non-empty blob. The
	//     sub_flow.drd_id column is reserved for a future explicit binding
	//     but is not the source of truth today (see U3 — flow_drd has a
	//     sub_flow_id column that holds the relation).
	drdExists := false
	if state, derr := deps.Repo.LoadYDocStateBySubFlow(ctx, sf.ID); derr == nil {
		drdExists = len(state) > 0
	} else if !errors.Is(derr, projects.ErrNotFound) {
		return Result{}, fmt.Errorf("resolve: load ydoc: %w", derr)
	}

	// 6. Build the result.
	fullSlug := sp.Slug + "/" + sf.Slug
	out := ResolveResult{
		Slug:  raw,
		Level: ResolveLevelSubFlow,
		SubFlow: ResolveSubFlow{
			ID:         sf.ID,
			Name:       sf.Name,
			Slug:       sf.Slug,
			SubProduct: sp.Name,
			FullSlug:   fullSlug,
		},
		PRDExists:          prdExists,
		FrameCount:         frameCount,
		StateCount:         stateCount,
		MixpanelEventNames: eventNames,
		DRDExists:          drdExists,
		PrototypeURL:       prototypeURL,
		CanvasLifecycle:    string(lifecycle),

		// Stubs — empty slices so JSON serializes as `[]`, never `null`.
		// Downstream teams adopt the convention; the shape is the contract.
		RecentEvents:     []ResolveEventOccurrence{},
		OpenSentryIssues: []ResolveSentryIssue{},
		StorybookPaths:   []string{},
		JIRAComponents:   []string{},

		Links: ResolveLinks{
			PRDViewerURL:      "/prd/" + fullSlug,
			FigmaURL:          figmaURL,
			ConventionsDocURL: conventionsDocURL,
		},
	}

	// 7. Narrow to a state when the caller supplied a 3-segment slug.
	//    Matching is best-effort: case-insensitive + trim on either the
	//    state's slug-form (label kebab-cased) or its raw frame_name. We
	//    do NOT error on miss — the State pointer stays nil and the
	//    caller sees the sub_flow-level result. This avoids breaking the
	//    common pre-skeleton case (PM types a state slug before the PRD
	//    has an auto-skeleton row for it).
	if len(parts) == 3 {
		out.Level = ResolveLevelState
		stateSeg := strings.ToLower(strings.TrimSpace(parts[2]))
		for _, st := range states {
			if matchesStateSeg(st, stateSeg) {
				frame := ""
				if st.FrameName != nil {
					frame = *st.FrameName
				}
				out.State = &ResolveState{
					ID:        st.ID,
					Label:     st.Label,
					Position:  st.Position,
					FrameName: frame,
				}
				break
			}
		}
	}

	// 8. Next-action hints. Mirror section.inspect / subflow.get style.
	hints := []NextAction{
		{
			Tool: "section.inspect",
			When: "to open the coverage wall for this sub_flow",
			InputHint: rawJSON(`{"sub_flow_slug": "` + fullSlug + `"}`),
		},
		{
			Tool: "drd.read",
			When: "to read the DRD",
			InputHint: rawJSON(`{"sub_flow_slug": "` + fullSlug + `"}`),
		},
		{
			Tool: "prd.author", Op: "get",
			When: "to read the PRD with current states",
			InputHint: rawJSON(`{"op": "get", "args": {"sub_flow_slug": "` + fullSlug + `"}}`),
		},
	}

	return Result{Data: out, NextActions: hints}, nil
}

// matchesStateSeg returns true when the supplied lowercased+trimmed state
// segment matches the PRDState by either:
//   - slug-form of the state Label (lower-case kebab of Label), or
//   - frame_name lowered+trimmed (verbatim or kebab-cased).
//
// Best-effort, intentionally lossy — the contract documents the slug
// shape (lower-case kebab of the state label) but we accept the raw
// frame name too because designers and PMs occasionally diverge on
// punctuation/spacing during early authoring.
func matchesStateSeg(st projects.PRDStateFull, seg string) bool {
	if seg == "" {
		return false
	}
	if kebab(st.Label) == seg {
		return true
	}
	if st.FrameName != nil {
		fn := strings.ToLower(strings.TrimSpace(*st.FrameName))
		if fn == seg || kebab(*st.FrameName) == seg {
			return true
		}
	}
	return false
}

// kebab is a small lower-case-kebab normaliser. Mirrors
// projects.subFlowSlugify but kept local to avoid exporting that helper.
func kebab(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return out
}
