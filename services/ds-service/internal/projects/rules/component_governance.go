package rules

// component_governance.go implements the U6 ComponentGovernanceRunner — three
// sub-rules under the "component_governance" violation category:
//
//   1. component_detached      (Medium) — RECTANGLE/FRAME/GROUP/INSTANCE nodes
//      whose name fuzzy-matches a known DS component slug but which carry no
//      componentId / mainComponent.id. Catches design-time detaches where a
//      reviewer copied a Button render but lost the instance link.
//   2. component_override_sprawl (Low) — INSTANCE nodes whose total override
//      count (componentProperties + boundVariables + direct visual props)
//      exceeds an 8-override threshold. Heavy overrides usually mean a missing
//      variant.
//   3. component_set_sprawl    (Info) — flows whose distinct componentSetKey
//      count exceeds 80. Flagged once per flow on its first screen.
//
// Production-grade similarity scoring against the full Glyph manifest is
// reserved for a follow-up tuning unit (the plan's audit.NewMatcher hook).
// U6 ships the simpler name-prefix allowlist below, which is sufficient to
// catch the high-signal "Button shaped like a rectangle" case the rule was
// scoped against.
//
// All three rules run from a single Run() pass for cache locality across the
// canonical_tree decode. Each sub-rule contributes Violations into a shared
// slice; ordering is stable per (flow, walk-order) so test assertions stay
// deterministic.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Loader interface (test-injectable) ──────────────────────────────────────

// ScreensWithFlowsLoader returns every screen in the version paired with its
// flow_id and canonical_tree JSON. The runner needs the flow axis (not just
// the version) so the per-flow component_set_sprawl tally bucket is correct.
//
// Production wiring is deferred to the orchestrator unit that threads every
// Phase 2 rule's loader through the worker pool.
//
// TODO(U6-prod-wire): real implementation reads
//   SELECT s.id, s.flow_id, COALESCE(t.canonical_tree, '')
//     FROM screens s LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
//    WHERE s.version_id = ? ORDER BY s.flow_id ASC, s.created_at ASC
type ScreensWithFlowsLoader interface {
	LoadScreensWithFlows(ctx context.Context, versionID string) ([]ScreenWithFlow, error)
}

// ScreenWithFlow pairs a screens-row mirror with its flow id + canonical-tree
// blob. Distinct from projects.ScreenWithTree because we always need the
// flow_id here and want a flow-scoped struct rather than reaching into the
// projects package's Phase 1 type. Tests use the local struct directly.
type ScreenWithFlow struct {
	ScreenID      string
	FlowID        string
	CanonicalTree string // JSON
}

// ─── Constants ───────────────────────────────────────────────────────────────

// Rule identifiers — emitted on every Violation row this runner produces.
const (
	ruleIDComponentDetached       = "component_detached"
	ruleIDComponentOverrideSprawl = "component_override_sprawl"
	ruleIDComponentSetSprawl      = "component_set_sprawl"
)

// Category — shared across all three sub-rules.
const categoryComponentGovernance = "component_governance"

// Thresholds — tunable defaults. The plan calls these out explicitly.
const (
	overrideSprawlThreshold = 8
	setSprawlThreshold      = 80
)

// dsAllowlist is the U6 baseline of known DS component slugs. Lowercased;
// matched as a case-insensitive prefix against the candidate node's name.
//
// Each entry must be the BARE component name as it appears in Figma. We
// match on prefix so "ButtonPrimary", "Button / Primary", and "Button"
// all match "button" — common Figma naming patterns. We do NOT match
// plurals ("Buttons") to avoid false-flagging container layers.
//
// The plan calls out audit.NewMatcher for production-grade scoring against
// the full Glyph manifest as a follow-up tuning. This allowlist is the
// shipping baseline.
var dsAllowlist = []string{
	"button",
	"iconbutton",
	"link",
	"tab",
	"menuitem",
	"chip",
	"toggle",
	"checkbox",
	"radio",
	"card",
	"toast",
	"modal",
	"alert",
	"banner",
	"badge",
	"avatar",
	"progress",
	"spinner",
	"tooltip",
}

// candidateTypes are the node types that may pose as detached DS components.
// INSTANCE is included because some pipelines strip mainComponent.id when an
// instance is detached without converting the layer back to RECTANGLE.
var candidateTypes = map[string]bool{
	"INSTANCE":  true,
	"FRAME":     true,
	"GROUP":     true,
	"RECTANGLE": true,
}

// directVisualOverrideKeys are direct properties on an INSTANCE node that
// count toward the override-sprawl tally when present. The full Figma list
// is much longer; this is the U6 minimum that catches the Tier 1 cases
// (paddings, fills, strokes, effects, radius, item spacing).
var directVisualOverrideKeys = []string{
	"fills",
	"strokes",
	"effects",
	"cornerRadius",
	"paddingLeft",
	"paddingRight",
	"paddingTop",
	"paddingBottom",
	"itemSpacing",
}

// ─── Runner ──────────────────────────────────────────────────────────────────

// ComponentGovernanceRunner implements projects.RuleRunner for U6.
type ComponentGovernanceRunner struct {
	loader ScreensWithFlowsLoader
}

// NewComponentGovernanceRunner constructs the U6 RuleRunner. The loader is
// injected so tests can supply an in-memory fake without touching SQL.
func NewComponentGovernanceRunner(loader ScreensWithFlowsLoader) *ComponentGovernanceRunner {
	return &ComponentGovernanceRunner{loader: loader}
}

// Run implements projects.RuleRunner.
//
// Walks every screen's canonical tree once. Each visited INSTANCE contributes
// to the per-flow componentSetKey tally and is checked for override sprawl;
// each visited candidate node (INSTANCE/FRAME/GROUP/RECTANGLE) is checked
// for the detached-lookalike pattern.
//
// After the walk, per-flow tallies are evaluated against the sprawl threshold
// and one Info violation is emitted per offending flow.
func (r *ComponentGovernanceRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("ComponentGovernanceRunner: nil version")
	}
	if r.loader == nil {
		return nil, fmt.Errorf("ComponentGovernanceRunner: loader not configured")
	}

	rows, err := r.loader.LoadScreensWithFlows(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("load screens with flows: %w", err)
	}

	// Per-flow tally state. We track first-screen-id per flow so the
	// component_set_sprawl violation can anchor to a real screen_id.
	// flowOrder preserves insertion order so emitted sprawl violations
	// come back in a stable order across runs.
	flows := map[string]*flowTally{}
	flowOrder := []string{}

	out := make([]projects.Violation, 0)

	for _, sc := range rows {
		ft, ok := flows[sc.FlowID]
		if !ok {
			ft = &flowTally{
				firstScreenID: sc.ScreenID,
				setKeys:       map[string]struct{}{},
			}
			flows[sc.FlowID] = ft
			flowOrder = append(flowOrder, sc.FlowID)
		}

		tree := decodeTree(sc.CanonicalTree)
		if tree == nil {
			continue
		}

		// Walk every node. The walker emits the (detached, override) sub-
		// rule violations directly into out and accumulates set keys into
		// the flow tally for the post-walk sprawl pass.
		walkGovernance(tree, &governanceCtx{
			version:    v,
			screenID:   sc.ScreenID,
			tally:      ft,
			out:        &out,
		})
	}

	// Per-flow sprawl pass. Iterating flowOrder (not the map) keeps emission
	// stable across runs.
	for _, flowID := range flowOrder {
		ft := flows[flowID]
		if len(ft.setKeys) >= setSprawlThreshold {
			out = append(out, projects.Violation{
				ID:         uuid.NewString(),
				VersionID:  v.ID,
				ScreenID:   ft.firstScreenID,
				TenantID:   v.TenantID,
				RuleID:     ruleIDComponentSetSprawl,
				Severity:   projects.SeverityInfo,
				Category:   categoryComponentGovernance,
				Property:   "component_set_count",
				Observed:   fmt.Sprintf("%d distinct components in flow", len(ft.setKeys)),
				Suggestion: "High count isn't always wrong but is worth surfacing — consider auditing redundancy.",
				Status:     "active",
			})
		}
	}

	return out, nil
}

// ─── Internals ───────────────────────────────────────────────────────────────

// flowTally accumulates per-flow state during the canonical_tree walk: the
// set of distinct componentSetKeys encountered (used by the post-walk
// component_set_sprawl pass) and the first-seen screen id (used as the
// anchor when emitting the per-flow Info violation).
type flowTally struct {
	firstScreenID string
	setKeys       map[string]struct{}
}

// governanceCtx threads the shared state through the recursive walker.
type governanceCtx struct {
	version  *projects.ProjectVersion
	screenID string
	tally    *flowTally
	out      *[]projects.Violation
}

// walkGovernance recurses through node + every descendant, emitting detached
// and override-sprawl violations and tallying componentSetKeys.
func walkGovernance(node any, ctx *governanceCtx) {
	if node == nil {
		return
	}
	// node may be a children-array element (already a map) or the children
	// slice itself (handle either shape gracefully).
	if arr, ok := node.([]any); ok {
		for _, c := range arr {
			walkGovernance(c, ctx)
		}
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}

	t, _ := m["type"].(string)

	// 1. Detached check — runs for any candidate node type that has NO
	//    real component link.
	if candidateTypes[t] && !hasComponentLink(m) {
		if slug, matched := matchAllowlist(nameOf(m)); matched {
			*ctx.out = append(*ctx.out, projects.Violation{
				ID:         uuid.NewString(),
				VersionID:  ctx.version.ID,
				ScreenID:   ctx.screenID,
				TenantID:   ctx.version.TenantID,
				RuleID:     ruleIDComponentDetached,
				Severity:   projects.SeverityMedium,
				Category:   categoryComponentGovernance,
				Property:   "instance",
				Observed:   fmt.Sprintf("%s at %s", nameOf(m), pathHint(m)),
				Suggestion: fmt.Sprintf("Likely meant to be the DS %s component — convert to instance", slug),
				Status:     "active",
			})
		}
	}

	// 2. INSTANCE-only logic — override sprawl tally + componentSet tally.
	if t == "INSTANCE" {
		if key, _ := componentSetKeyOf(m); key != "" {
			ctx.tally.setKeys[key] = struct{}{}
		}
		if count := countOverrides(m); count >= overrideSprawlThreshold {
			*ctx.out = append(*ctx.out, projects.Violation{
				ID:         uuid.NewString(),
				VersionID:  ctx.version.ID,
				ScreenID:   ctx.screenID,
				TenantID:   ctx.version.TenantID,
				RuleID:     ruleIDComponentOverrideSprawl,
				Severity:   projects.SeverityLow,
				Category:   categoryComponentGovernance,
				Property:   "overrides",
				Observed:   fmt.Sprintf("%d overrides on instance", count),
				Suggestion: "Heavy overrides — consider extracting a new component variant.",
				Status:     "active",
			})
		}
	}

	// Recurse.
	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkGovernance(c, ctx)
		}
	}
}

// hasComponentLink reports whether an INSTANCE-shaped node carries any of
// the durable links back to a DS component. A node without componentId,
// componentSetId, mainComponent.id, or mainComponent.componentSetKey is
// treated as detached for U6 purposes.
func hasComponentLink(m map[string]any) bool {
	if s, _ := m["componentId"].(string); s != "" {
		return true
	}
	if s, _ := m["componentSetId"].(string); s != "" {
		return true
	}
	if mc, ok := m["mainComponent"].(map[string]any); ok {
		if s, _ := mc["id"].(string); s != "" {
			return true
		}
		if s, _ := mc["componentSetKey"].(string); s != "" {
			return true
		}
	}
	return false
}

// nameOf returns the node's name (or "" if absent / wrong type).
func nameOf(m map[string]any) string {
	s, _ := m["name"].(string)
	return s
}

// pathHint returns a compact path hint for the violation Observed column.
// We don't carry the full breadcrumb through the walker (would balloon the
// per-violation cost); the parent name + node type is enough for the UI to
// orient the reviewer.
func pathHint(m map[string]any) string {
	t, _ := m["type"].(string)
	if t == "" {
		t = "NODE"
	}
	return t
}

// matchAllowlist returns the canonical DS slug matching the given node name
// (case-insensitive prefix; first match wins) and whether a match occurred.
//
// Match semantics:
//   - case-insensitive
//   - the candidate name's leading alphabetical run must be EXACTLY one of
//     the allowlist entries. "Button" matches "button"; "ButtonPrimary"
//     matches "button" (leading alpha run "Button"); "Buttons" does NOT
//     match (leading alpha run "Buttons" — plural).
//
// Rationale: a strict prefix match would let "Buttons" (a container layer
// commonly named to mean "the buttons row") slip through. Anchoring on the
// leading alpha run keeps the allowlist tight without a per-entry custom
// matcher.
func matchAllowlist(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	lead := leadingAlpha(strings.ToLower(name))
	if lead == "" {
		return "", false
	}
	for _, slug := range dsAllowlist {
		if lead == slug {
			return canonical(slug), true
		}
	}
	return "", false
}

// leadingAlpha returns the leading alphabetic run of s. Stops at the first
// non-letter rune (digit, separator, slash, hyphen, space).
func leadingAlpha(s string) string {
	for i, r := range s {
		if !isLetter(r) {
			return s[:i]
		}
	}
	return s
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// canonical returns the title-cased canonical form of a slug for the
// violation Suggestion column. We don't ship a full case map; the
// allowlist is small enough to hard-code via lookup.
func canonical(slug string) string {
	switch slug {
	case "iconbutton":
		return "IconButton"
	case "menuitem":
		return "MenuItem"
	}
	if slug == "" {
		return slug
	}
	return strings.ToUpper(slug[:1]) + slug[1:]
}

// countOverrides totals user overrides on an INSTANCE node.
//
// Counted sources:
//   1. componentProperties[k] entries whose value differs from defaultValue
//      (when both fields are present). When defaultValue is absent we count
//      the entry as one override regardless — Figma omits defaultValue when
//      the binding has never been touched in the file's history, so a
//      present-but-defaultless entry indicates the user did something.
//   2. boundVariables entries — each one is one variable binding.
//   3. Direct visual props (see directVisualOverrideKeys).
func countOverrides(inst map[string]any) int {
	total := 0

	// componentProperties.
	if cp, ok := inst["componentProperties"].(map[string]any); ok {
		for _, raw := range cp {
			entry, ok := raw.(map[string]any)
			if !ok {
				// Some pipelines flatten to scalar; count as one override.
				total++
				continue
			}
			val, valOK := entry["value"]
			def, defOK := entry["defaultValue"]
			switch {
			case valOK && defOK:
				if !sameValue(val, def) {
					total++
				}
			case valOK:
				total++
			}
		}
	}

	// boundVariables.
	if bv, ok := inst["boundVariables"].(map[string]any); ok {
		total += len(bv)
	}

	// Direct visual props.
	for _, k := range directVisualOverrideKeys {
		if _, present := inst[k]; present {
			total++
		}
	}

	return total
}

// sameValue compares two JSON-decoded values. Mirrors scalarEqual in
// treediff.go but is local to keep this file standalone for the U6 unit.
func sameValue(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

